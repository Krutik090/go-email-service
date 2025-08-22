// // cmd/email-api/handlers.go
package main

// import (
//     "net/http"
//     "strconv"
//     "fmt"
//     "time"
    
//     "github.com/gin-gonic/gin"
    
//     "phishkit-email-service/internal/queue"
//     "phishkit-email-service/internal/smtp"
// )

// // Campaign Launch Handler
// func (api *EmailAPI) launchCampaign(c *gin.Context) {
//     var req struct {
//         CampaignID    string                   `json:"campaign_id" binding:"required"`
//         Targets       []queue.Target          `json:"targets" binding:"required"`
//         Template      queue.EmailTemplate     `json:"template" binding:"required"`
//         SMTPProfile   string                  `json:"smtp_profile" binding:"required"`
//         Settings      queue.CampaignSettings  `json:"settings"`
//     }

//     if err := c.ShouldBindJSON(&req); err != nil {
//         api.logger.Errorf("Invalid request data: %v", err)
//         c.JSON(http.StatusBadRequest, gin.H{
//             "error": "Invalid request data",
//             "details": err.Error(),
//         })
//         return
//     }

//     api.logger.Infof("Launching campaign %s with %d targets", req.CampaignID, len(req.Targets))

//     // Validate SMTP profile exists
//     err := api.smtpManager.HealthCheck(req.SMTPProfile)
//     if err != nil {
//         api.logger.Errorf("SMTP profile %s health check failed: %v", req.SMTPProfile, err)
//         c.JSON(http.StatusBadRequest, gin.H{
//             "error": "SMTP profile not available",
//             "profile": req.SMTPProfile,
//         })
//         return
//     }

//     // Set default settings if not provided
//     if req.Settings.BatchSize == 0 {
//         req.Settings.BatchSize = 100
//     }
//     if req.Settings.RateLimitPerMin == 0 {
//         req.Settings.RateLimitPerMin = 1000
//     }
//     if req.Settings.RetryAttempts == 0 {
//         req.Settings.RetryAttempts = 3
//     }
//     if req.Settings.Priority == "" {
//         req.Settings.Priority = "medium"
//     }

//     // Enqueue the campaign
//     jobID, err := api.queueManager.EnqueueBulkEmail(
//         req.CampaignID,
//         req.Targets,
//         req.Template,
//         req.SMTPProfile,
//         req.Settings,
//     )
//     if err != nil {
//         api.logger.Errorf("Failed to enqueue campaign %s: %v", req.CampaignID, err)
//         c.JSON(http.StatusInternalServerError, gin.H{
//             "error": "Failed to enqueue email job",
//             "details": err.Error(),
//         })
//         return
//     }

//     // Calculate estimated completion time
//     estimatedMinutes := float64(len(req.Targets)) / float64(req.Settings.RateLimitPerMin) * 60
//     estimatedCompletion := time.Now().Add(time.Duration(estimatedMinutes) * time.Minute)

//     c.JSON(http.StatusAccepted, gin.H{
//         "job_id": jobID,
//         "campaign_id": req.CampaignID,
//         "status": "queued",
//         "total_targets": len(req.Targets),
//         "estimated_completion": estimatedCompletion,
//     })
// }

// // Campaign Status Handler
// func (api *EmailAPI) getCampaignStatus(c *gin.Context) {
//     campaignID := c.Param("campaignId")
    
//     if campaignID == "" {
//         c.JSON(http.StatusBadRequest, gin.H{"error": "Campaign ID is required"})
//         return
//     }

//     status, err := api.queueManager.GetCampaignStatus(campaignID)
//     if err != nil {
//         api.logger.Errorf("Failed to get campaign status for %s: %v", campaignID, err)
//         c.JSON(http.StatusNotFound, gin.H{
//             "error": "Campaign not found",
//             "campaign_id": campaignID,
//         })
//         return
//     }

//     c.JSON(http.StatusOK, status)
// }

// // Campaign Results Handler
// func (api *EmailAPI) getCampaignResults(c *gin.Context) {
//     campaignID := c.Param("campaignId")
    
//     if campaignID == "" {
//         c.JSON(http.StatusBadRequest, gin.H{"error": "Campaign ID is required"})
//         return
//     }

//     // Get campaign status first
//     status, err := api.queueManager.GetCampaignStatus(campaignID)
//     if err != nil {
//         c.JSON(http.StatusNotFound, gin.H{
//             "error": "Campaign not found",
//             "campaign_id": campaignID,
//         })
//         return
//     }

//     // ✅ FIXED: Get detailed job results using getter methods
//     jobIDs, err := api.queueManager.GetSetMembers(fmt.Sprintf("campaign:%s:jobs", campaignID))
//     if err != nil {
//         c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch job IDs"})
//         return
//     }

//     var results []map[string]interface{}
//     totalSent := 0
//     totalFailed := 0

//     for _, jobID := range jobIDs {
//         // Get batch results for this job
//         pattern := fmt.Sprintf("job:%s_batch_*:status", jobID)
//         batchKeys, err := api.queueManager.GetKeys(pattern)
//         if err != nil {
//             continue
//         }

//         for _, batchKey := range batchKeys {
//             batchData, err := api.queueManager.GetHashAll(batchKey)
//             if err != nil || len(batchData) == 0 {
//                 continue
//             }

//             batchResult := map[string]interface{}{
//                 "batch_id": batchKey,
//                 "status": batchData["status"],
//             }

//             if sentStr, ok := batchData["sent"]; ok {
//                 if sent, err := strconv.Atoi(sentStr); err == nil {
//                     batchResult["sent"] = sent
//                     totalSent += sent
//                 }
//             }

//             if failedStr, ok := batchData["failed"]; ok {
//                 if failed, err := strconv.Atoi(failedStr); err == nil {
//                     batchResult["failed"] = failed
//                     totalFailed += failed
//                 }
//             }

//             results = append(results, batchResult)
//         }
//     }

//     c.JSON(http.StatusOK, gin.H{
//         "campaign_id": campaignID,
//         "summary": status,
//         "batch_results": results,
//         "totals": gin.H{
//             "sent": totalSent,
//             "failed": totalFailed,
//         },
//     })
// }

// // SMTP Profile Management
// func (api *EmailAPI) addSMTPProfile(c *gin.Context) {
//     var config smtp.SMTPConfig
    
//     if err := c.ShouldBindJSON(&config); err != nil {
//         c.JSON(http.StatusBadRequest, gin.H{
//             "error": "Invalid SMTP configuration",
//             "details": err.Error(),
//         })
//         return
//     }

//     err := api.smtpManager.AddProfile(config)
//     if err != nil {
//         api.logger.Errorf("Failed to add SMTP profile %s: %v", config.ProfileID, err)
//         c.JSON(http.StatusInternalServerError, gin.H{
//             "error": "Failed to add SMTP profile",
//             "details": err.Error(),
//         })
//         return
//     }

//     c.JSON(http.StatusCreated, gin.H{
//         "message": "SMTP profile added successfully",
//         "profile_id": config.ProfileID,
//     })
// }

// // SMTP Health Check
// func (api *EmailAPI) checkSMTPHealth(c *gin.Context) {
//     profileID := c.Param("profileId")
    
//     err := api.smtpManager.HealthCheck(profileID)
//     if err != nil {
//         c.JSON(http.StatusServiceUnavailable, gin.H{
//             "profile_id": profileID,
//             "healthy": false,
//             "error": err.Error(),
//         })
//         return
//     }

//     c.JSON(http.StatusOK, gin.H{
//         "profile_id": profileID,
//         "healthy": true,
//     })
// }

// // SMTP Statistics
// func (api *EmailAPI) getSMTPStats(c *gin.Context) {
//     profileID := c.Param("profileId")
    
//     stats := api.smtpManager.GetPoolStats(profileID)
//     if stats == nil {
//         c.JSON(http.StatusNotFound, gin.H{
//             "error": "SMTP profile not found",
//             "profile_id": profileID,
//         })
//         return
//     }

//     c.JSON(http.StatusOK, stats)
// }

// // ✅ FIXED: System Health Check
// func (api *EmailAPI) healthCheck(c *gin.Context) {
//     // Check Redis connection using getter method
//     redisStatus := "healthy"
//     err := api.queueManager.PingRedis()
//     if err != nil {
//         redisStatus = "unhealthy: " + err.Error()
//     }

//     // Check queue sizes using getter method
//     queueSizes := make(map[string]int64)
//     queues := []string{"email_queue_high", "email_queue_medium", "email_queue_low"}
    
//     for _, queue := range queues {
//         size, err := api.queueManager.GetListLength(queue)
//         if err != nil {
//             size = -1
//         }
//         queueSizes[queue] = size
//     }

//     c.JSON(http.StatusOK, gin.H{
//         "status": "healthy",
//         "service": "phishkit-email-service",
//         "version": "1.0.0",
//         "timestamp": time.Now(),
//         "redis_status": redisStatus,
//         "queue_sizes": queueSizes,
//     })
// }

// // ✅ FIXED: System Metrics
// func (api *EmailAPI) getMetrics(c *gin.Context) {
//     // Get Redis info using getter method
//     redisInfo, err := api.queueManager.GetRedisInfo()
//     redisConnected := err == nil

//     // Get queue statistics
//     totalQueued := int64(0)
//     queueDetails := make(map[string]interface{})
    
//     queues := []string{"email_queue_high", "email_queue_medium", "email_queue_low"}
//     for _, queue := range queues {
//         size, err := api.queueManager.GetListLength(queue)
//         if err == nil {
//             totalQueued += size
//             queueDetails[queue] = size
//         }
//     }

//     // Get active campaigns count using getter method
//     activeCampaigns, _ := api.queueManager.GetKeys("campaign:*:stats")

//     c.JSON(http.StatusOK, gin.H{
//         "service": "phishkit-email-service",
//         "timestamp": time.Now(),
//         "redis": gin.H{
//             "connected": redisConnected,
//             "info_available": len(redisInfo) > 0,
//         },
//         "queues": gin.H{
//             "total_queued": totalQueued,
//             "details": queueDetails,
//         },
//         "campaigns": gin.H{
//             "active_count": len(activeCampaigns),
//         },
//     })
// }
