package checker

import (
	"context"
	"fmt"
	"strings"
	"time"

	"uptime-monitor/internal/db"
	"uptime-monitor/internal/logger"
	"uptime-monitor/internal/model"
	"uptime-monitor/internal/notifier"
)

type Coordinator struct {
	httpProber *HTTPProber
	tcpProber  *TCPProber
	smtpProber *SMTPProber

	monitorRepo *db.MonitorRepo
	checkRepo   *db.CheckRepo
	notifier    *notifier.Dispatcher
	logger      *logger.Logger
}

func NewCoordinator(
	mRepo *db.MonitorRepo,
	cRepo *db.CheckRepo,
	notif *notifier.Dispatcher,
	log *logger.Logger,
) *Coordinator {
	return &Coordinator{
		httpProber:  NewHTTPProber(),
		tcpProber:   NewTCPProber(),
		smtpProber:  NewSMTPProber(),
		monitorRepo: mRepo,
		checkRepo:   cRepo,
		notifier:    notif,
		logger:      log,
	}
}

// ExecuteCheck performs a probe on the monitor, applies retry damping, updates DB, and triggers alerts.
func (c *Coordinator) ExecuteCheck(ctx context.Context, m *model.Monitor) *model.CheckResult {
	protocol := strings.ToLower(strings.TrimSpace(m.Protocol))
	var result *model.CheckResult

	switch protocol {
	case "tcp":
		result = c.tcpProber.Probe(ctx, m)
	case "smtp":
		result = c.smtpProber.Probe(ctx, m)
	case "http", "https":
		fallthrough
	default:
		result = c.httpProber.Probe(ctx, m)
	}

	prevStatus := m.CurrentStatus
	retryThreshold := m.RetryCount
	if retryThreshold <= 0 {
		retryThreshold = 2
	}

	newStatus := prevStatus
	newFailures := m.ConsecutiveFailures

	if result.IsSuccess {
		newFailures = 0
		newStatus = "up"
	} else {
		newFailures++
		if newFailures >= retryThreshold {
			newStatus = "down"
		} else {
			newStatus = "degraded"
		}
	}

	// Persist check log
	logEntry := &model.CheckLog{
		MonitorID:      m.ID,
		StatusCode:     result.StatusCode,
		LatencyMs:      result.LatencyMs,
		IsSuccess:      result.IsSuccess,
		ErrorType:      result.ErrorType,
		ErrorMessage:   result.ErrorMessage,
		FailureDetails: result.FailureDetails,
		CheckedAt:      time.Now().UTC(),
	}
	_ = c.checkRepo.LogCheck(logEntry)

	// Update monitor state in DB
	_ = c.monitorRepo.UpdateStatus(m.ID, newStatus, newFailures, result.LatencyMs)

	// Emit structured log
	if !result.IsSuccess {
		probePhase := ""
		if result.FailureDetails != nil {
			probePhase = result.FailureDetails.ProbePhase
		}
		c.logger.MonitorWarn(m.ID, "checker", fmt.Sprintf("Check failed (%s): %s", result.ErrorType, result.ErrorMessage), map[string]any{
			"status":       newStatus,
			"failures":     newFailures,
			"threshold":    retryThreshold,
			"latency_ms":   result.LatencyMs,
			"probe_phase":  probePhase,
			"status_code":  result.StatusCode,
		})
	} else {
		c.logger.MonitorInfo(m.ID, "checker", fmt.Sprintf("Check succeeded in %dms (status: %d)", result.LatencyMs, result.StatusCode), map[string]any{
			"latency_ms": result.LatencyMs,
			"status":     newStatus,
		})
	}

	// Trigger notifications on status transitions
	if prevStatus != newStatus && prevStatus != "pending" {
		event := newStatus // "down", "degraded", or "up"
		c.notifier.Notify(ctx, model.WebhookContext{
			Event:          event,
			MonitorID:      m.ID,
			MonitorName:    m.Name,
			Target:         m.Target,
			Protocol:       m.Protocol,
			Status:         newStatus,
			PreviousStatus: prevStatus,
			LatencyMs:      result.LatencyMs,
			ErrorMessage:   result.ErrorMessage,
			Timestamp:      time.Now().UTC().Format(time.RFC3339),
		})
	}

	// Check TLS cert expiration for admin warning (e.g. <= 14 days)
	if result.FailureDetails != nil && result.FailureDetails.CertExpiryDays > 0 && result.FailureDetails.CertExpiryDays <= 14 {
		c.logger.MonitorWarn(m.ID, "checker", fmt.Sprintf("SSL certificate expires in %d days (%s)", result.FailureDetails.CertExpiryDays, result.FailureDetails.CertExpiry), map[string]any{
			"cert_issuer":      result.FailureDetails.CertIssuer,
			"cert_expiry_days": result.FailureDetails.CertExpiryDays,
		})
		c.notifier.Notify(ctx, model.WebhookContext{
			Event:          "tls_warning",
			MonitorID:      m.ID,
			MonitorName:    m.Name,
			Target:         m.Target,
			Protocol:       m.Protocol,
			Status:         newStatus,
			CertExpiryDays: result.FailureDetails.CertExpiryDays,
			ErrorMessage:   fmt.Sprintf("TLS certificate expires in %d days", result.FailureDetails.CertExpiryDays),
			Timestamp:      time.Now().UTC().Format(time.RFC3339),
		})
	}

	return result
}
