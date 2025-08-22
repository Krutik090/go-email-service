package config

import (
    "os"
    "strconv"
    "github.com/spf13/viper"
)

type Config struct {
    Server   ServerConfig
    Redis    RedisConfig
    MongoDB  MongoDBConfig
    Email    EmailConfig
}

type ServerConfig struct {
    Port     string
    Host     string
    Mode     string
}

type RedisConfig struct {
    Host     string
    Port     string
    Password string
    DB       int
}

type MongoDBConfig struct {
    URI        string
    Database   string
    Username   string
    Password   string
}

type EmailConfig struct {
    MaxWorkers      int
    BatchSize       int
    RetryAttempts   int
    RateLimitPerMin int
}

func Load() (*Config, error) {
    viper.AutomaticEnv()
    
    config := &Config{
        Server: ServerConfig{
            Port: getEnvOrDefault("PORT", "8080"),
            Host: getEnvOrDefault("HOST", "0.0.0.0"),
            Mode: getEnvOrDefault("GIN_MODE", "release"),
        },
        Redis: RedisConfig{
            Host:     getEnvOrDefault("REDIS_HOST", "localhost"),
            Port:     getEnvOrDefault("REDIS_PORT", "6379"),
            Password: getEnvOrDefault("REDIS_PASSWORD", ""),
            DB:       getEnvIntOrDefault("REDIS_DB", 0),
        },
        MongoDB: MongoDBConfig{
            URI:      getEnvOrDefault("MONGODB_URI", "mongodb://localhost:27017"),
            Database: getEnvOrDefault("MONGODB_DATABASE", "phishKit"),
            Username: getEnvOrDefault("MONGODB_USERNAME", ""),
            Password: getEnvOrDefault("MONGODB_PASSWORD", ""),
        },
        Email: EmailConfig{
            MaxWorkers:      getEnvIntOrDefault("EMAIL_MAX_WORKERS", 25),
            BatchSize:       getEnvIntOrDefault("EMAIL_BATCH_SIZE", 500),
            RetryAttempts:   getEnvIntOrDefault("EMAIL_RETRY_ATTEMPTS", 3),
            RateLimitPerMin: getEnvIntOrDefault("EMAIL_RATE_LIMIT", 10000),
        },
    }
    
    return config, nil
}

func getEnvOrDefault(key, defaultValue string) string {
    if value := os.Getenv(key); value != "" {
        return value
    }
    return defaultValue
}

func getEnvIntOrDefault(key string, defaultValue int) int {
    if value := os.Getenv(key); value != "" {
        if intValue, err := strconv.Atoi(value); err == nil {
            return intValue
        }
    }
    return defaultValue
}
