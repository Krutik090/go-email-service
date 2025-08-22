// cmd/email-api/main.go
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"
    "strings"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"

	"phishkit-email-service/config"
	"phishkit-email-service/internal/email"
	"phishkit-email-service/internal/queue"
	"phishkit-email-service/internal/smtp"
)

type EmailAPI struct {
	smtpManager    *smtp.SMTPManager
	queueManager   *queue.QueueManager
	emailProcessor *email.EmailProcessor
	logger         *logrus.Logger
}

func main() {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Setup logger
	logger := logrus.New()
	if cfg.Server.Mode == "debug" {
		logger.SetLevel(logrus.DebugLevel)
		gin.SetMode(gin.DebugMode)
	} else {
		logger.SetLevel(logrus.InfoLevel)
		gin.SetMode(gin.ReleaseMode)
	}

	// Setup Redis client
	redisClient := redis.NewClient(&redis.Options{
		Addr:     cfg.Redis.Host + ":" + cfg.Redis.Port,
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})

	// Test Redis connection
	ctx := context.Background()
	_, err = redisClient.Ping(ctx).Result()
	if err != nil {
		log.Fatalf("Failed to connect to Redis: %v", err)
	}
	logger.Info("Connected to Redis successfully")

	// Initialize services
	smtpManager := smtp.NewSMTPManager(logger)
	queueManager := queue.NewQueueManager(redisClient, logger)
	emailProcessor := email.NewEmailProcessor(smtpManager, logger)

	api := &EmailAPI{
		smtpManager:    smtpManager,
		queueManager:   queueManager,
		emailProcessor: emailProcessor,
		logger:         logger,
	}

	// Start worker goroutines
	numWorkers := cfg.Email.MaxWorkers
	logger.Infof("Starting %d email workers", numWorkers)

	for i := 0; i < numWorkers; i++ {
		go api.emailWorker(i)
	}

	// Setup HTTP server
	router := gin.Default()

	// CORS middleware
	router.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"http://localhost:5000", "http://localhost:5173"},
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization"},
		AllowCredentials: true,
	}))

	// Routes
	api.setupRoutes(router)

	// Start server
	addr := cfg.Server.Host + ":" + cfg.Server.Port
	logger.Infof("🚀 Starting PhishKit Email Service on %s", addr)
	log.Fatal(router.Run(addr))
}

func (api *EmailAPI) emailWorker(workerID int) {
	api.logger.Infof("Email worker %d started", workerID)

	for {
		// Dequeue job
		job, err := api.queueManager.DequeueJob()
		if err != nil {
			api.logger.Errorf("Worker %d: Failed to dequeue job: %v", workerID, err)
			continue
		}

		if job == nil {
			continue // No job available, continue polling
		}

		api.logger.Infof("Worker %d: Processing job %s", workerID, job.BatchID)

		// Process the job
		result, err := api.emailProcessor.ProcessEmailBatch(job)
		if err != nil {
			api.logger.Errorf("Worker %d: Failed to process job %s: %v", workerID, job.BatchID, err)

			// Retry job
			api.queueManager.RetryJob(*job, job.Settings.RetryAttempts)
			continue
		}

		// Complete the job
		err = api.queueManager.CompleteJob(*result)
		if err != nil {
			api.logger.Errorf("Worker %d: Failed to complete job %s: %v", workerID, job.BatchID, err)
		}
	}
}

func (api *EmailAPI) setupRoutes(router *gin.Engine) {
	// Health check
	router.GET("/health", api.healthCheck)

	// Campaign management
	router.POST("/campaign/launch", api.launchCampaign)
	router.GET("/campaign/status/:campaignId", api.getCampaignStatus)
	router.GET("/campaign/results/:campaignId", api.getCampaignResults)

	// SMTP profile management
	router.GET("/smtp/profiles", api.listSMTPProfiles)
	router.POST("/smtp/profile", api.addSMTPProfile)
	router.GET("/smtp/profile/:profileId/health", api.checkSMTPHealth)
	router.GET("/smtp/profile/:profileId/stats", api.getSMTPStats)
	router.DELETE("/smtp/profile/:profileId", api.deleteSMTPProfile)

	// System metrics
	router.GET("/metrics", api.getMetrics)
}

// ===== HANDLER METHODS =====

func (api *EmailAPI) launchCampaign(c *gin.Context) {
    // ✅ FIXED: Struct with proper validation for nested objects
    var req struct {
        CampaignID    string                   `json:"campaign_id" binding:"required"`
        Targets       []queue.Target          `json:"targets" binding:"required,dive"`
        Template      queue.EmailTemplate     `json:"template" binding:"required"`
        SMTPProfile   string                  `json:"smtp_profile" binding:"required"`
        Settings      queue.CampaignSettings  `json:"settings"`
        TrackingUrls  queue.TrackingUrls      `json:"tracking_urls"`
    }

    api.logger.Infof("🎯 Campaign launch handler called!")
    api.logger.Infof("📤 Request method: %s", c.Request.Method)
    api.logger.Infof("📤 Request path: %s", c.Request.URL.Path)
    api.logger.Infof("📤 Content-Type: %s", c.GetHeader("Content-Type"))

    if err := c.ShouldBindJSON(&req); err != nil {
        api.logger.Errorf("❌ JSON binding failed: %v", err)
        
        // ✅ FIXED: Better error handling with JSON field names
        var ve validator.ValidationErrors
        if errors.As(err, &ve) {
            errorsMap := make(map[string]string)
            for _, fe := range ve {
                // Map struct field names to JSON field names
                var fieldName string
                switch fe.Field() {
                case "CampaignID":
                    fieldName = "campaign_id"
                case "SMTPProfile":
                    fieldName = "smtp_profile"
                case "Targets":
                    fieldName = "targets"
                case "Template":
                    fieldName = "template"
                case "TrackingUrls":
                    fieldName = "tracking_urls"
                default:
                    fieldName = strings.ToLower(fe.Field())
                }
                errorsMap[fieldName] = fmt.Sprintf("%s is %s", fieldName, fe.Tag())
            }
            api.logger.Errorf("❌ Validation errors: %v", errorsMap)
            c.JSON(http.StatusBadRequest, gin.H{
                "error": "Validation failed",
                "details": errorsMap,
            })
            return
        }
        
        c.JSON(http.StatusBadRequest, gin.H{
            "error": "Invalid request data",
            "details": err.Error(),
        })
        return
    }

    // ✅ Log received data
    api.logger.Infof("✅ Successfully parsed request data:")
    api.logger.Infof("   CampaignID: %s", req.CampaignID)
    api.logger.Infof("   SMTPProfile: %s", req.SMTPProfile)
    api.logger.Infof("   Targets: %d", len(req.Targets))
    api.logger.Infof("   Template: name=%s, subject=%s", req.Template.Name, req.Template.Subject)

    // Validate SMTP profile exists
    if !api.smtpManager.ProfileExists(req.SMTPProfile) {
        api.logger.Errorf("❌ SMTP profile not found: %s", req.SMTPProfile)
        c.JSON(http.StatusBadRequest, gin.H{
            "error": "SMTP profile not available",
            "profile": req.SMTPProfile,
        })
        return
    }

    // Set default settings
    if req.Settings.BatchSize == 0 {
        req.Settings.BatchSize = 100
    }
    if req.Settings.RateLimitPerMin == 0 {
        req.Settings.RateLimitPerMin = 1000
    }
    if req.Settings.RetryAttempts == 0 {
        req.Settings.RetryAttempts = 3
    }
    if req.Settings.Priority == "" {
        req.Settings.Priority = "medium"
    }

    api.logger.Infof("🚀 Enqueuing campaign with settings: batch_size=%d, rate_limit=%d", 
        req.Settings.BatchSize, req.Settings.RateLimitPerMin)

    // Enqueue the campaign
    jobID, err := api.queueManager.EnqueueBulkEmail(
        req.CampaignID,
        req.Targets,
        req.Template,
        req.SMTPProfile,
        req.Settings,
        req.TrackingUrls,
    )

    if err != nil {
        api.logger.Errorf("❌ Failed to enqueue campaign %s: %v", req.CampaignID, err)
        c.JSON(http.StatusInternalServerError, gin.H{
            "error": "Failed to enqueue email job",
            "details": err.Error(),
        })
        return
    }

    // Calculate estimated completion time
    estimatedMinutes := float64(len(req.Targets)) / float64(req.Settings.RateLimitPerMin) * 60
    estimatedCompletion := time.Now().Add(time.Duration(estimatedMinutes) * time.Minute)

    api.logger.Infof("✅ Campaign successfully launched: jobID=%s", jobID)

    c.JSON(http.StatusAccepted, gin.H{
        "job_id": jobID,
        "campaign_id": req.CampaignID,
        "status": "queued",
        "total_targets": len(req.Targets),
        "estimated_completion": estimatedCompletion,
    })
}

// Campaign Status Handler
func (api *EmailAPI) getCampaignStatus(c *gin.Context) {
	campaignID := c.Param("campaignId")

	if campaignID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Campaign ID is required"})
		return
	}

	status, err := api.queueManager.GetCampaignStatus(campaignID)
	if err != nil {
		api.logger.Errorf("Failed to get campaign status for %s: %v", campaignID, err)
		c.JSON(http.StatusNotFound, gin.H{
			"error":       "Campaign not found",
			"campaign_id": campaignID,
		})
		return
	}

	c.JSON(http.StatusOK, status)
}

// Campaign Results Handler
func (api *EmailAPI) getCampaignResults(c *gin.Context) {
	campaignID := c.Param("campaignId")

	if campaignID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Campaign ID is required"})
		return
	}

	// Get campaign status first
	status, err := api.queueManager.GetCampaignStatus(campaignID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error":       "Campaign not found",
			"campaign_id": campaignID,
		})
		return
	}

	// Get detailed job results
	jobIDs, err := api.queueManager.GetSetMembers(fmt.Sprintf("campaign:%s:jobs", campaignID))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch job IDs"})
		return
	}

	var results []map[string]interface{}
	totalSent := 0
	totalFailed := 0

	for _, jobID := range jobIDs {
		// Get batch results for this job
		pattern := fmt.Sprintf("job:%s_batch_*:status", jobID)
		batchKeys, err := api.queueManager.GetKeys(pattern)
		if err != nil {
			continue
		}

		for _, batchKey := range batchKeys {
			batchData, err := api.queueManager.GetHashAll(batchKey)
			if err != nil || len(batchData) == 0 {
				continue
			}

			batchResult := map[string]interface{}{
				"batch_id": batchKey,
				"status":   batchData["status"],
			}

			if sentStr, ok := batchData["sent"]; ok {
				if sent, err := strconv.Atoi(sentStr); err == nil {
					batchResult["sent"] = sent
					totalSent += sent
				}
			}

			if failedStr, ok := batchData["failed"]; ok {
				if failed, err := strconv.Atoi(failedStr); err == nil {
					batchResult["failed"] = failed
					totalFailed += failed
				}
			}

			results = append(results, batchResult)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"campaign_id":   campaignID,
		"summary":       status,
		"batch_results": results,
		"totals": gin.H{
			"sent":   totalSent,
			"failed": totalFailed,
		},
	})
}

// SMTP Profile Management
func (api *EmailAPI) addSMTPProfile(c *gin.Context) {
	var config smtp.SMTPConfig

	if err := c.ShouldBindJSON(&config); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Invalid SMTP configuration",
			"details": err.Error(),
		})
		return
	}

	err := api.smtpManager.AddProfile(config)
	if err != nil {
		api.logger.Errorf("Failed to add SMTP profile %s: %v", config.ProfileID, err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to add SMTP profile",
			"details": err.Error(),
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"message":    "SMTP profile added successfully",
		"profile_id": config.ProfileID,
	})
}

// SMTP Health Check
func (api *EmailAPI) checkSMTPHealth(c *gin.Context) {
	profileID := c.Param("profileId")

	err := api.smtpManager.HealthCheck(profileID)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"profile_id": profileID,
			"healthy":    false,
			"error":      err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"profile_id": profileID,
		"healthy":    true,
	})
}

// SMTP Statistics
func (api *EmailAPI) getSMTPStats(c *gin.Context) {
	profileID := c.Param("profileId")

	stats := api.smtpManager.GetPoolStats(profileID)
	if stats == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error":      "SMTP profile not found",
			"profile_id": profileID,
		})
		return
	}

	c.JSON(http.StatusOK, stats)
}

// System Health Check
func (api *EmailAPI) healthCheck(c *gin.Context) {
	// Check Redis connection
	redisStatus := "healthy"
	err := api.queueManager.PingRedis()
	if err != nil {
		redisStatus = "unhealthy: " + err.Error()
	}

	// Check queue sizes
	queueSizes := make(map[string]int64)
	queues := []string{"email_queue_high", "email_queue_medium", "email_queue_low"}

	for _, queue := range queues {
		size, err := api.queueManager.GetListLength(queue)
		if err != nil {
			size = -1
		}
		queueSizes[queue] = size
	}

	c.JSON(http.StatusOK, gin.H{
		"status":       "healthy",
		"service":      "phishkit-email-service",
		"version":      "1.0.0",
		"timestamp":    time.Now(),
		"redis_status": redisStatus,
		"queue_sizes":  queueSizes,
	})
}

// System Metrics
func (api *EmailAPI) getMetrics(c *gin.Context) {
	// Get Redis info
	redisInfo, err := api.queueManager.GetRedisInfo()
	redisConnected := err == nil

	// Get queue statistics
	totalQueued := int64(0)
	queueDetails := make(map[string]interface{})

	queues := []string{"email_queue_high", "email_queue_medium", "email_queue_low"}
	for _, queue := range queues {
		size, err := api.queueManager.GetListLength(queue)
		if err == nil {
			totalQueued += size
			queueDetails[queue] = size
		}
	}

	// Get active campaigns count
	activeCampaigns, _ := api.queueManager.GetKeys("campaign:*:stats")

	c.JSON(http.StatusOK, gin.H{
		"service":   "phishkit-email-service",
		"timestamp": time.Now(),
		"redis": gin.H{
			"connected":      redisConnected,
			"info_available": len(redisInfo) > 0,
		},
		"queues": gin.H{
			"total_queued": totalQueued,
			"details":      queueDetails,
		},
		"campaigns": gin.H{
			"active_count": len(activeCampaigns),
		},
	})
}

// List SMTP Profiles Handler
func (api *EmailAPI) listSMTPProfiles(c *gin.Context) {
	profiles := api.smtpManager.ListProfiles()

	c.JSON(http.StatusOK, gin.H{
		"profiles": profiles,
		"count":    len(profiles),
	})
}

// Delete SMTP Profile Handler
func (api *EmailAPI) deleteSMTPProfile(c *gin.Context) {
	profileID := c.Param("profileId")

	if profileID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Profile ID is required",
		})
		return
	}

	err := api.smtpManager.DeleteProfile(profileID)
	if err != nil {
		api.logger.Errorf("Failed to delete SMTP profile %s: %v", profileID, err)
		c.JSON(http.StatusNotFound, gin.H{
			"error":      err.Error(),
			"profile_id": profileID,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":    "SMTP profile deleted successfully",
		"profile_id": profileID,
	})
}
