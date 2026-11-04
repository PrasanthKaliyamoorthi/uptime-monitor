package checker

import (
	"context"
	"fmt"
	"sync"
	"time"

	"uptime-monitor/internal/db"
	"uptime-monitor/internal/logger"
	"uptime-monitor/internal/model"
)

type Scheduler struct {
	coordinator *Coordinator
	monitorRepo *db.MonitorRepo
	logger      *logger.Logger

	mu        sync.Mutex
	running   bool
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	checkSem  chan struct{} // limits concurrent active checks
	nextCheck map[string]time.Time
	inFlight  map[string]bool
}

func NewScheduler(coord *Coordinator, mRepo *db.MonitorRepo, log *logger.Logger, maxWorkers int) *Scheduler {
	if maxWorkers <= 0 {
		maxWorkers = 20
	}
	return &Scheduler{
		coordinator: coord,
		monitorRepo: mRepo,
		logger:      log,
		checkSem:    make(chan struct{}, maxWorkers),
		nextCheck:   make(map[string]time.Time),
		inFlight:    make(map[string]bool),
	}
}

// Start begins the background monitoring loop.
func (s *Scheduler) Start(parentCtx context.Context) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	ctx, cancel := context.WithCancel(parentCtx)
	s.cancel = cancel
	s.mu.Unlock()

	s.logger.Info("scheduler", "Starting background monitoring scheduler", nil)

	s.wg.Add(1)
	go s.loop(ctx)
}

func (s *Scheduler) loop(ctx context.Context) {
	defer s.wg.Done()
	// Ticker runs every 1 second, evaluating which monitors are due for a check
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("scheduler", "Monitoring scheduler stopped", nil)
			return
		case <-ticker.C:
			monitors, err := s.monitorRepo.GetActive()
			if err != nil {
				continue
			}

			now := time.Now().UTC()
			s.mu.Lock()
			for _, m := range monitors {
				mon := m
				if s.inFlight[mon.ID] {
					continue
				}

				due, exists := s.nextCheck[mon.ID]
				if !exists || now.After(due) {
					interval := time.Duration(mon.IntervalSec) * time.Second
					if interval < 5*time.Second {
						interval = 60 * time.Second
					}
					s.nextCheck[mon.ID] = now.Add(interval)
					s.inFlight[mon.ID] = true

					// Dispatch check job
					s.dispatchCheckLocked(ctx, &mon)
				}
			}
			s.mu.Unlock()
		}
	}
}

func (s *Scheduler) dispatchCheckLocked(ctx context.Context, m *model.Monitor) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() {
			s.mu.Lock()
			delete(s.inFlight, m.ID)
			s.mu.Unlock()
		}()
		defer func() {
			if r := recover(); r != nil {
				s.logger.Error("scheduler", fmt.Sprintf("Recovered from panic during check for monitor %s: %v", m.ID, r), nil)
			}
		}()

		// Acquire worker slot
		select {
		case s.checkSem <- struct{}{}:
			defer func() { <-s.checkSem }()
		case <-ctx.Done():
			return
		}

		s.coordinator.ExecuteCheck(ctx, m)
	}()
}

// TriggerNow executes an immediate one-off check for a monitor and resets its next scheduled check.
func (s *Scheduler) TriggerNow(ctx context.Context, monitorID string) (*model.CheckResult, error) {
	m, err := s.monitorRepo.GetByID(monitorID)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	if s.inFlight[monitorID] {
		s.mu.Unlock()
		// Another check is already in-flight for this monitor, don't duplicate
		return &model.CheckResult{
			IsSuccess:    true,
			ErrorMessage: "check already in progress",
		}, nil
	}
	s.inFlight[monitorID] = true
	interval := time.Duration(m.IntervalSec) * time.Second
	if interval < 5*time.Second {
		interval = 60 * time.Second
	}
	s.nextCheck[monitorID] = time.Now().UTC().Add(interval)
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.inFlight, monitorID)
		s.mu.Unlock()
	}()
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("scheduler", fmt.Sprintf("Recovered from panic during TriggerNow for monitor %s: %v", monitorID, r), nil)
		}
	}()

	return s.coordinator.ExecuteCheck(ctx, m), nil
}

// Stop gracefully shuts down the scheduler.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()

	s.wg.Wait()
}
