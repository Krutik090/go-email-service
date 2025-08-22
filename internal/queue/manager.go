// internal/queue/manager.go
package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
)

type Target struct {
	UID       string `json:"uid"`
	Email     string `json:"email"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Position  string `json:"position"`
}

type EmailTemplate struct {
	Name    string `json:"name"`
	Subject string `json:"subject"`
	HTML    string `json:"html"`
	Text    string `json:"text"`
}

type TrackingUrls struct {
	OpenTrackingUrl  string `json:"open_tracking_url"`
	ClickTrackingUrl string `json:"click_tracking_url"`
	LandingPageUrl   string `json:"landing_page_url"`
}

type EmailJob struct {
	JobID        string           `json:"job_id"`
	BatchID      string           `json:"batch_id"`
	CampaignID   string           `json:"campaign_id"`
	Targets      []Target         `json:"targets"`
	Template     EmailTemplate    `json:"template"`
	SMTPProfile  string           `json:"smtp_profile"`
	Settings     CampaignSettings `json:"settings"`
	TrackingUrls TrackingUrls     `json:"tracking_urls"` // ← ADD THIS
	CreatedAt    time.Time        `json:"created_at"`
	Attempts     int              `json:"attempts"`
}

type CampaignSettings struct {
	BatchSize       int    `json:"batch_size"`
	RateLimitPerMin int    `json:"rate_limit_per_min"`
	RetryAttempts   int    `json:"retry_attempts"`
	Priority        string `json:"priority"`
}

type JobResult struct {
	JobID       string    `json:"job_id"`
	BatchID     string    `json:"batch_id"`
	CampaignID  string    `json:"campaign_id"`
	Success     bool      `json:"success"`
	Sent        int       `json:"sent"`
	Failed      int       `json:"failed"`
	Errors      []string  `json:"errors"`
	ProcessedAt time.Time `json:"processed_at"`
}

type QueueManager struct {
	Redis  *redis.Client // ✅ Exported field
	ctx    context.Context
	logger *logrus.Logger
}

func NewQueueManager(redisClient *redis.Client, logger *logrus.Logger) *QueueManager {
	return &QueueManager{
		Redis:  redisClient, //  Use capital R
		ctx:    context.Background(),
		logger: logger,
	}
}

// ✅ ADD: Getter methods for external access
func (qm *QueueManager) GetRedisClient() *redis.Client {
	return qm.Redis
}

func (qm *QueueManager) GetContext() context.Context {
	return qm.ctx
}

// ✅ ADD: Convenience methods for common Redis operations
func (qm *QueueManager) GetKeys(pattern string) ([]string, error) {
	return qm.Redis.Keys(qm.ctx, pattern).Result()
}

func (qm *QueueManager) GetSetMembers(key string) ([]string, error) {
	return qm.Redis.SMembers(qm.ctx, key).Result()
}

func (qm *QueueManager) GetHashAll(key string) (map[string]string, error) {
	return qm.Redis.HGetAll(qm.ctx, key).Result()
}

func (qm *QueueManager) GetListLength(key string) (int64, error) {
	return qm.Redis.LLen(qm.ctx, key).Result()
}

func (qm *QueueManager) PingRedis() error {
	return qm.Redis.Ping(qm.ctx).Err()
}

func (qm *QueueManager) GetRedisInfo() (string, error) {
	return qm.Redis.Info(qm.ctx).Result()
}

func (qm *QueueManager) EnqueueBulkEmail(campaignID string, targets []Target, template EmailTemplate, smtpProfile string, settings CampaignSettings, trackingUrls TrackingUrls) (string, error) {
	jobID := uuid.New().String()

	// Split targets into batches
	batches := qm.createBatches(targets, settings.BatchSize)

	qm.logger.Infof("Creating %d batches for campaign %s", len(batches), campaignID)

	// Enqueue each batch
	for i, batch := range batches {
		batchID := fmt.Sprintf("%s_batch_%d", jobID, i)

		job := EmailJob{
			JobID:        jobID,
			BatchID:      batchID,
			CampaignID:   campaignID,
			Targets:      batch,
			Template:     template,
			SMTPProfile:  smtpProfile,
			Settings:     settings,
			TrackingUrls: trackingUrls, // ← ADD THIS
			CreatedAt:    time.Now(),
			Attempts:     0,
		}

		jobData, err := json.Marshal(job)
		if err != nil {
			return "", fmt.Errorf("failed to marshal job: %v", err)
		}

		// Add to priority queue based on settings
		queueName := qm.getQueueName(settings.Priority)

		err = qm.Redis.LPush(qm.ctx, queueName, jobData).Err()
		if err != nil {
			return "", fmt.Errorf("failed to enqueue job: %v", err)
		}

		// Set job status
		statusKey := fmt.Sprintf("job:%s:status", batchID)
		qm.Redis.HSet(qm.ctx, statusKey, map[string]interface{}{
			"status":     "queued",
			"created_at": time.Now().Unix(),
			"attempts":   0,
		})
		qm.Redis.Expire(qm.ctx, statusKey, 24*time.Hour) // Expire after 24 hours
	}

	// Set campaign job tracking
	campaignKey := fmt.Sprintf("campaign:%s:jobs", campaignID)
	qm.Redis.SAdd(qm.ctx, campaignKey, jobID)
	qm.Redis.Expire(qm.ctx, campaignKey, 48*time.Hour)

	// Set campaign statistics
	statsKey := fmt.Sprintf("campaign:%s:stats", campaignID)
	qm.Redis.HSet(qm.ctx, statsKey, map[string]interface{}{
		"total_targets": len(targets),
		"total_batches": len(batches),
		"sent":          0,
		"failed":        0,
		"pending":       len(targets),
		"status":        "processing",
		"started_at":    time.Now().Unix(),
	})
	qm.Redis.Expire(qm.ctx, statsKey, 48*time.Hour)

	qm.logger.Infof("Enqueued campaign %s with job ID %s (%d batches)", campaignID, jobID, len(batches))

	return jobID, nil
}

func (qm *QueueManager) DequeueJob() (*EmailJob, error) {
	// Try high priority queue first
	queues := []string{"email_queue_high", "email_queue_medium", "email_queue_low"}

	for _, queueName := range queues {
		result, err := qm.Redis.BRPop(qm.ctx, 1*time.Second, queueName).Result()
		if err == redis.Nil {
			continue // No jobs in this queue
		} else if err != nil {
			return nil, fmt.Errorf("failed to dequeue job: %v", err)
		}

		// Parse job data
		var job EmailJob
		err = json.Unmarshal([]byte(result[1]), &job)
		if err != nil {
			qm.logger.Errorf("Failed to unmarshal job: %v", err)
			continue
		}

		// Update job status
		statusKey := fmt.Sprintf("job:%s:status", job.BatchID)
		qm.Redis.HSet(qm.ctx, statusKey, map[string]interface{}{
			"status":     "processing",
			"started_at": time.Now().Unix(),
			"attempts":   job.Attempts + 1,
		})

		return &job, nil
	}

	return nil, nil // No jobs available
}

func (qm *QueueManager) CompleteJob(result JobResult) error {
	// Update job status
	statusKey := fmt.Sprintf("job:%s:status", result.BatchID)
	status := "completed"
	if !result.Success {
		status = "failed"
	}

	qm.Redis.HSet(qm.ctx, statusKey, map[string]interface{}{
		"status":       status,
		"completed_at": time.Now().Unix(),
		"sent":         result.Sent,
		"failed":       result.Failed,
	})

	// Update campaign statistics
	statsKey := fmt.Sprintf("campaign:%s:stats", result.CampaignID)
	qm.Redis.HIncrBy(qm.ctx, statsKey, "sent", int64(result.Sent))
	qm.Redis.HIncrBy(qm.ctx, statsKey, "failed", int64(result.Failed))
	qm.Redis.HIncrBy(qm.ctx, statsKey, "pending", int64(-(result.Sent + result.Failed)))

	// Check if campaign is complete
	stats, err := qm.Redis.HGetAll(qm.ctx, statsKey).Result()
	if err == nil {
		pending := stats["pending"]
		if pending == "0" {
			qm.Redis.HSet(qm.ctx, statsKey, "status", "completed")
			qm.Redis.HSet(qm.ctx, statsKey, "completed_at", time.Now().Unix())
		}
	}

	qm.logger.Infof("Completed batch %s: sent=%d, failed=%d", result.BatchID, result.Sent, result.Failed)

	return nil
}

func (qm *QueueManager) RetryJob(job EmailJob, maxRetries int) error {
	if job.Attempts >= maxRetries {
		// Max retries reached, mark as failed
		result := JobResult{
			JobID:       job.JobID,
			BatchID:     job.BatchID,
			CampaignID:  job.CampaignID,
			Success:     false,
			Sent:        0,
			Failed:      len(job.Targets),
			Errors:      []string{"Max retries exceeded"},
			ProcessedAt: time.Now(),
		}
		return qm.CompleteJob(result)
	}

	job.Attempts++

	// Re-enqueue with delay
	jobData, err := json.Marshal(job)
	if err != nil {
		return err
	}

	// Add to retry queue with exponential backoff delay
	queueName := qm.getQueueName(job.Settings.Priority)

	// For now, just re-add to queue (in production, you might want delayed queue)
	return qm.Redis.LPush(qm.ctx, queueName, jobData).Err()
}

func (qm *QueueManager) GetCampaignStatus(campaignID string) (map[string]interface{}, error) {
	statsKey := fmt.Sprintf("campaign:%s:stats", campaignID)

	stats, err := qm.Redis.HGetAll(qm.ctx, statsKey).Result()
	if err != nil {
		return nil, err
	}

	if len(stats) == 0 {
		return nil, fmt.Errorf("campaign not found: %s", campaignID)
	}

	return map[string]interface{}{
		"campaign_id":  campaignID,
		"status":       stats["status"],
		"total":        stats["total_targets"],
		"sent":         stats["sent"],
		"failed":       stats["failed"],
		"pending":      stats["pending"],
		"started_at":   stats["started_at"],
		"completed_at": stats["completed_at"],
	}, nil
}

func (qm *QueueManager) createBatches(targets []Target, batchSize int) [][]Target {
	var batches [][]Target

	for i := 0; i < len(targets); i += batchSize {
		end := i + batchSize
		if end > len(targets) {
			end = len(targets)
		}
		batches = append(batches, targets[i:end])
	}

	return batches
}

func (qm *QueueManager) getQueueName(priority string) string {
	switch priority {
	case "high":
		return "email_queue_high"
	case "low":
		return "email_queue_low"
	default:
		return "email_queue_medium"
	}
}
