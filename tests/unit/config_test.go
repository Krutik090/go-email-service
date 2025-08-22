package tests

import (
    "os"
    "testing"
    "phishkit-email-service/config"
    "github.com/stretchr/testify/assert"
)

func TestConfigLoad(t *testing.T) {
    // Set test environment variables
    os.Setenv("PORT", "9999")
    os.Setenv("REDIS_HOST", "test-redis")
    
    cfg, err := config.Load()
    
    assert.NoError(t, err)
    assert.Equal(t, "9999", cfg.Server.Port)
    assert.Equal(t, "test-redis", cfg.Redis.Host)
    
    // Cleanup
    os.Unsetenv("PORT")
    os.Unsetenv("REDIS_HOST")
}
