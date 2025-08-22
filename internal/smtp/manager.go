// internal/smtp/manager.go
package smtp

import (
    "crypto/tls"
    "fmt"
    "net/smtp"
    "sync"
    "time"
	"net"
    "github.com/sirupsen/logrus"
)

type SMTPConfig struct {
    ProfileID       string `json:"profile_id"`
    Host           string `json:"host"`
    Port           int    `json:"port"`
    Username       string `json:"username"`
    Password       string `json:"password"`
    FromAddress    string `json:"from_address"`
    FromName       string `json:"from_name"`
    UseTLS         bool   `json:"use_tls"`
    IgnoreCertErrors bool `json:"ignore_cert_errors"`
    MaxConnections  int   `json:"max_connections"`
    RateLimit      int    `json:"rate_limit"` // emails per minute
}

type SMTPConnection struct {
    Client    *smtp.Client
    Config    SMTPConfig
    LastUsed  time.Time
    InUse     bool
    Created   time.Time
}

type ConnectionPool struct {
    connections chan *SMTPConnection
    config      SMTPConfig
    maxConns    int
    currentConns int
    mutex       sync.RWMutex
    healthCheck  time.Time
}

type SMTPManager struct {
    pools   map[string]*ConnectionPool
    mutex   sync.RWMutex
    logger  *logrus.Logger
}

func NewSMTPManager(logger *logrus.Logger) *SMTPManager {
    return &SMTPManager{
        pools:  make(map[string]*ConnectionPool),
        logger: logger,
    }
}

func (sm *SMTPManager) AddProfile(config SMTPConfig) error {
    sm.mutex.Lock()
    defer sm.mutex.Unlock()
    
    if config.MaxConnections == 0 {
        config.MaxConnections = 5 // Default pool size
    }
    
    pool := &ConnectionPool{
        connections:  make(chan *SMTPConnection, config.MaxConnections),
        config:       config,
        maxConns:     config.MaxConnections,
        currentConns: 0,
    }
    
    // Pre-fill pool with connections
    for i := 0; i < 2; i++ { // Start with 2 connections
        conn, err := sm.createConnection(config)
        if err != nil {
            sm.logger.Errorf("Failed to create initial SMTP connection: %v", err)
            continue
        }
        
        select {
        case pool.connections <- conn:
            pool.currentConns++
        default:
            conn.Client.Close()
        }
    }
    
    sm.pools[config.ProfileID] = pool
    sm.logger.Infof("Added SMTP profile: %s with %d connections", config.ProfileID, pool.currentConns)
    
    return nil
}

func (sm *SMTPManager) GetConnection(profileID string) (*SMTPConnection, error) {
    sm.mutex.RLock()
    pool, exists := sm.pools[profileID]
    sm.mutex.RUnlock()
    
    if !exists {
        return nil, fmt.Errorf("SMTP profile not found: %s", profileID)
    }
    
    // Try to get existing connection from pool
    select {
    case conn := <-pool.connections:
        // Check if connection is still valid
        if time.Since(conn.LastUsed) > 5*time.Minute {
            conn.Client.Close()
            // Create new connection
            newConn, err := sm.createConnection(pool.config)
            if err != nil {
                return nil, err
            }
            conn = newConn
        }
        
        conn.InUse = true
        conn.LastUsed = time.Now()
        return conn, nil
        
    default:
        // No available connection, create new one if under limit
        pool.mutex.Lock()
        if pool.currentConns < pool.maxConns {
            conn, err := sm.createConnection(pool.config)
            if err != nil {
                pool.mutex.Unlock()
                return nil, err
            }
            
            pool.currentConns++
            pool.mutex.Unlock()
            
            conn.InUse = true
            conn.LastUsed = time.Now()
            return conn, nil
        }
        pool.mutex.Unlock()
        
        // Wait for available connection
        conn := <-pool.connections
        conn.InUse = true
        conn.LastUsed = time.Now()
        return conn, nil
    }
}

func (sm *SMTPManager) ReturnConnection(profileID string, conn *SMTPConnection) {
    sm.mutex.RLock()
    pool, exists := sm.pools[profileID]
    sm.mutex.RUnlock()
    
    if !exists {
        conn.Client.Close()
        return
    }
    
    conn.InUse = false
    conn.LastUsed = time.Now()
    
    select {
    case pool.connections <- conn:
        // Successfully returned to pool
    default:
        // Pool is full, close connection
        conn.Client.Close()
        pool.mutex.Lock()
        pool.currentConns--
        pool.mutex.Unlock()
    }
}

func (sm *SMTPManager) createConnection(config SMTPConfig) (*SMTPConnection, error) {
    addr := fmt.Sprintf("%s:%d", config.Host, config.Port)
    
    // Step 1: Establish plain TCP connection
    conn, err := net.Dial("tcp", addr)
    if err != nil {
        return nil, fmt.Errorf("TCP dial failed: %v", err)
    }
    
    // Step 2: Create SMTP client over the connection
    client, err := smtp.NewClient(conn, config.Host)
    if err != nil {
        conn.Close()
        return nil, fmt.Errorf("SMTP client creation failed: %v", err)
    }
    
    // Step 3: Upgrade to TLS using STARTTLS (if required)
    if config.UseTLS {
        tlsConfig := &tls.Config{
            ServerName:         config.Host,
            InsecureSkipVerify: config.IgnoreCertErrors,
        }
        
        if err := client.StartTLS(tlsConfig); err != nil {
            client.Close()
            return nil, fmt.Errorf("STARTTLS failed: %v", err)
        }
        
        sm.logger.Debugf("STARTTLS successful for %s:%d", config.Host, config.Port)
    }
    
    // Step 4: Authenticate if credentials provided
    if config.Username != "" && config.Password != "" {
        auth := smtp.PlainAuth("", config.Username, config.Password, config.Host)
        if err := client.Auth(auth); err != nil {
            client.Close()
            return nil, fmt.Errorf("SMTP authentication failed: %v", err)
        }
        
        sm.logger.Debugf("SMTP authentication successful for %s", config.Username)
    }
    
    sm.logger.Debugf("SMTP connection created successfully for %s:%d", config.Host, config.Port)
    
    return &SMTPConnection{
        Client:   client,
        Config:   config,
        LastUsed: time.Now(),
        Created:  time.Now(),
        InUse:    false,
    }, nil
}

func (sm *SMTPManager) HealthCheck(profileID string) error {
    conn, err := sm.GetConnection(profileID)
    if err != nil {
        return err
    }
    defer sm.ReturnConnection(profileID, conn)
    
    // Try to send NOOP command
    if err := conn.Client.Noop(); err != nil {
        return fmt.Errorf("SMTP health check failed: %v", err)
    }
    
    return nil
}

func (sm *SMTPManager) GetPoolStats(profileID string) map[string]interface{} {
    sm.mutex.RLock()
    pool, exists := sm.pools[profileID]
    sm.mutex.RUnlock()
    
    if !exists {
        return nil
    }
    
    pool.mutex.RLock()
    defer pool.mutex.RUnlock()
    
    return map[string]interface{}{
        "profile_id":       profileID,
        "max_connections":  pool.maxConns,
        "current_connections": pool.currentConns,
        "available_connections": len(pool.connections),
        "rate_limit":       pool.config.RateLimit,
    }
}

func (sm *SMTPManager) ListProfiles() []map[string]interface{} {
    sm.mutex.RLock()
    defer sm.mutex.RUnlock()
    
    profiles := make([]map[string]interface{}, 0)
    
    for profileID, pool := range sm.pools {
        pool.mutex.RLock()
        profiles = append(profiles, map[string]interface{}{
            "profile_id": profileID,
            "host": pool.config.Host,
            "port": pool.config.Port,
            "from_address": pool.config.FromAddress,
            "max_connections": pool.config.MaxConnections,
            "current_connections": pool.currentConns,
            "available_connections": len(pool.connections),
        })
        pool.mutex.RUnlock()
    }
    
    return profiles
}

// DeleteProfile removes an SMTP profile
func (sm *SMTPManager) DeleteProfile(profileID string) error {
    sm.mutex.Lock()
    defer sm.mutex.Unlock()
    
    pool, exists := sm.pools[profileID]
    if !exists {
        return fmt.Errorf("SMTP profile not found: %s", profileID)
    }
    
    sm.logger.Infof("Deleting SMTP profile: %s", profileID)
    
    // Close all connections safely
    pool.mutex.Lock()
    defer pool.mutex.Unlock()
    
    // Close all existing connections
    for {
        select {
        case conn := <-pool.connections:
            if conn.Client != nil {
                conn.Client.Quit()
                conn.Client.Close()
            }
        default:
            goto done
        }
    }
done:
    close(pool.connections)
    
    // Remove from manager
    delete(sm.pools, profileID)
    
    sm.logger.Infof("Successfully deleted SMTP profile: %s", profileID)
    return nil
}

// ProfileExists checks if a profile exists
func (sm *SMTPManager) ProfileExists(profileID string) bool {
    sm.mutex.RLock()
    defer sm.mutex.RUnlock()
    
    _, exists := sm.pools[profileID]
    return exists
}