// internal/email/processor.go
package email

import (
    "fmt"
    "net/url"
    "regexp"
    "strings"
    "time"
    // "net/mail"

    "phishkit-email-service/internal/queue"
    "phishkit-email-service/internal/smtp"

    "github.com/sirupsen/logrus"
)

type EmailProcessor struct {
    smtpManager *smtp.SMTPManager
    logger      *logrus.Logger
}

func NewEmailProcessor(smtpManager *smtp.SMTPManager, logger *logrus.Logger) *EmailProcessor {
    return &EmailProcessor{
        smtpManager: smtpManager,
        logger:      logger,
    }
}

func (ep *EmailProcessor) ProcessEmailBatch(job *queue.EmailJob) (*queue.JobResult, error) {
    startTime := time.Now()
    result := &queue.JobResult{
        JobID:       job.JobID,
        BatchID:     job.BatchID,
        CampaignID:  job.CampaignID,
        Success:     true,
        Sent:        0,
        Failed:      0,
        Errors:      []string{},
        ProcessedAt: time.Now(),
    }

    ep.logger.Infof("Processing batch %s with %d targets", job.BatchID, len(job.Targets))

    // Get SMTP connection
    conn, err := ep.smtpManager.GetConnection(job.SMTPProfile)
    if err != nil {
        result.Success = false
        result.Failed = len(job.Targets)
        result.Errors = append(result.Errors, fmt.Sprintf("SMTP connection failed: %v", err))
        return result, err
    }
    defer ep.smtpManager.ReturnConnection(job.SMTPProfile, conn)

    // Process each target in the batch
    for _, target := range job.Targets {
        // ✅ FIXED: Pass all required parameters
        success := ep.sendEmailToTarget(conn, target, job.Template, job.Settings, job.TrackingUrls, job.CampaignID)
        if success {
            result.Sent++
        } else {
            result.Failed++
        }

        // Rate limiting - delay between emails
        if job.Settings.RateLimitPerMin > 0 {
            delay := time.Duration(60000/job.Settings.RateLimitPerMin) * time.Millisecond
            time.Sleep(delay)
        }
    }

    processingTime := time.Since(startTime)
    ep.logger.Infof("Batch %s completed in %v: sent=%d, failed=%d",
        job.BatchID, processingTime, result.Sent, result.Failed)

    if result.Failed > 0 {
        result.Success = false
    }

    return result, nil
}

func (ep *EmailProcessor) sendEmailToTarget(conn *smtp.SMTPConnection, target queue.Target, template queue.EmailTemplate, settings queue.CampaignSettings, trackingUrls queue.TrackingUrls, campaignID string) bool {
    maxRetries := settings.RetryAttempts

    for attempt := 0; attempt < maxRetries; attempt++ {
        if attempt > 0 {
            // Exponential backoff for retries
            backoffDelay := time.Duration(attempt*attempt) * time.Second
            time.Sleep(backoffDelay)
            ep.logger.Debugf("Retrying email to %s (attempt %d)", target.Email, attempt+1)
        }

        // ✅ FIXED: Pass all required parameters including trackingUrls and campaignID
        if ep.attemptEmailSend(conn, target, template, trackingUrls, campaignID) {
            if attempt > 0 {
                ep.logger.Infof("Email to %s succeeded on retry %d", target.Email, attempt+1)
            }
            return true
        }
    }

    ep.logger.Errorf("Email to %s failed after %d attempts", target.Email, maxRetries)
    return false
}

// ✅ FIXED: Updated function signature to include trackingUrls and campaignID
func (ep *EmailProcessor) attemptEmailSend(conn *smtp.SMTPConnection, target queue.Target, template queue.EmailTemplate, trackingUrls queue.TrackingUrls, campaignID string) bool {
    // Prepare email content
    subject := ep.personalizeContent(template.Subject, target)
    htmlBody := ep.personalizeContent(template.HTML, target)
    textBody := ep.personalizeContent(template.Text, target)

    // ✅ FIXED: Add tracking to HTML content
    if htmlBody != "" {
        htmlBody = ep.addTrackingPixel(htmlBody, target.UID, campaignID, trackingUrls)
        htmlBody = ep.addClickTracking(htmlBody, target.UID, campaignID, trackingUrls)
    }

    // Create email message
    var message strings.Builder

    // Headers
    message.WriteString(fmt.Sprintf("From: %s <%s>\r\n", conn.Config.FromName, conn.Config.FromAddress))
    message.WriteString(fmt.Sprintf("To: %s\r\n", target.Email))
    message.WriteString(fmt.Sprintf("Subject: %s\r\n", subject))
    message.WriteString("MIME-Version: 1.0\r\n")

    if htmlBody != "" && textBody != "" {
        // Multipart message
        boundary := fmt.Sprintf("boundary-%d", time.Now().Unix())
        message.WriteString(fmt.Sprintf("Content-Type: multipart/alternative; boundary=\"%s\"\r\n\r\n", boundary))

        // Text part
        message.WriteString(fmt.Sprintf("--%s\r\n", boundary))
        message.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
        message.WriteString("Content-Transfer-Encoding: 7bit\r\n\r\n")
        message.WriteString(textBody)
        message.WriteString("\r\n\r\n")

        // HTML part
        message.WriteString(fmt.Sprintf("--%s\r\n", boundary))
        message.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
        message.WriteString("Content-Transfer-Encoding: 7bit\r\n\r\n")
        message.WriteString(htmlBody)
        message.WriteString("\r\n\r\n")

        message.WriteString(fmt.Sprintf("--%s--\r\n", boundary))
    } else if htmlBody != "" {
        // HTML only
        message.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
        message.WriteString("Content-Transfer-Encoding: 7bit\r\n\r\n")
        message.WriteString(htmlBody)
    } else {
        // Text only
        message.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
        message.WriteString("Content-Transfer-Encoding: 7bit\r\n\r\n")
        message.WriteString(textBody)
    }

    // Send email
    err := conn.Client.Mail(conn.Config.FromAddress)
    if err != nil {
        ep.logger.Errorf("MAIL command failed for %s: %v", target.Email, err)
        return false
    }

    err = conn.Client.Rcpt(target.Email)
    if err != nil {
        ep.logger.Errorf("RCPT command failed for %s: %v", target.Email, err)
        return false
    }

    writer, err := conn.Client.Data()
    if err != nil {
        ep.logger.Errorf("DATA command failed for %s: %v", target.Email, err)
        return false
    }

    _, err = writer.Write([]byte(message.String()))
    if err != nil {
        ep.logger.Errorf("Writing email data failed for %s: %v", target.Email, err)
        writer.Close()
        return false
    }

    err = writer.Close()
    if err != nil {
        ep.logger.Errorf("Closing email data failed for %s: %v", target.Email, err)
        return false
    }

    return true
}

// ✅ ADDED: Tracking pixel injection method
func (ep *EmailProcessor) addTrackingPixel(html, uid, campaignID string, trackingUrls queue.TrackingUrls) string {
    if trackingUrls.OpenTrackingUrl == "" {
        return html
    }

    // Create tracking pixel URL
    trackingPixel := fmt.Sprintf(
        `<img src="%s/%s/%s" width="1" height="1" style="display:none;" alt="" />`,
        trackingUrls.OpenTrackingUrl, campaignID, uid,
    )

    // Insert before closing body tag
    if strings.Contains(html, "</body>") {
        return strings.Replace(html, "</body>", trackingPixel+"</body>", 1)
    }

    // If no body tag, append at the end
    return html + trackingPixel
}

// ✅ ADDED: Click tracking injection method
func (ep *EmailProcessor) addClickTracking(html, uid, campaignID string, trackingUrls queue.TrackingUrls) string {
    if trackingUrls.ClickTrackingUrl == "" {
        return html
    }

    // Replace all href attributes with tracking links
    re := regexp.MustCompile(`href="([^"]*)"`)

    return re.ReplaceAllStringFunc(html, func(match string) string {
        // Extract the original URL
        originalUrl := strings.TrimPrefix(match, `href="`)
        originalUrl = strings.TrimSuffix(originalUrl, `"`)

        // Skip if it's already a tracking URL or empty/relative
        if strings.Contains(originalUrl, trackingUrls.ClickTrackingUrl) ||
            originalUrl == "" ||
            strings.HasPrefix(originalUrl, "#") ||
            strings.HasPrefix(originalUrl, "mailto:") {
            return match
        }

        // Create tracked URL
        trackedUrl := fmt.Sprintf(
            `href="%s/%s/%s?url=%s"`,
            trackingUrls.ClickTrackingUrl,
            campaignID,
            uid,
            url.QueryEscape(originalUrl),
        )

        return trackedUrl
    })
}

func (ep *EmailProcessor) personalizeContent(content string, target queue.Target) string {
    replacements := map[string]string{
        "{{first_name}}": target.FirstName,
        "{{last_name}}":  target.LastName,
        "{{email}}":      target.Email,
        "{{position}}":   target.Position,
        "{{uid}}":        target.UID,
        "{{name}}":       fmt.Sprintf("%s %s", target.FirstName, target.LastName),
    }

    result := content
    for placeholder, value := range replacements {
        result = strings.ReplaceAll(result, placeholder, value)
    }

    return result
}
