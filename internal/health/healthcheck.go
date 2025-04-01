package health

import (
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/sven-borkert/b2b-ingress-manager/internal/database"
	"github.com/sven-borkert/b2b-ingress-manager/internal/models"

	"github.com/sirupsen/logrus"
)

// Checker handles health checks for backends
type Checker struct {
	db           *database.Service
	logger       *logrus.Logger
	checkTimeout time.Duration
	interval     time.Duration
	stop         chan struct{}
	wg           sync.WaitGroup
}

// Config for the health checker
type Config struct {
	CheckTimeout time.Duration
	Interval     time.Duration
}

// NewChecker creates a new health checker
func NewChecker(db *database.Service, config Config, logger *logrus.Logger) *Checker {
	return &Checker{
		db:           db,
		logger:       logger,
		checkTimeout: config.CheckTimeout,
		interval:     config.Interval,
		stop:         make(chan struct{}),
	}
}

// Start begins the health check process
func (c *Checker) Start() {
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		ticker := time.NewTicker(c.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if err := c.checkAllBackends(); err != nil {
					c.logger.Errorf("Error during health check: %v", err)
				}
			case <-c.stop:
				c.logger.Info("Health checker stopped")
				return
			}
		}
	}()
	c.logger.Info("Health checker started")
}

// Stop ends the health check process
func (c *Checker) Stop() {
	close(c.stop)
	c.wg.Wait()
}

// checkAllBackends performs health checks on all backend addresses
func (c *Checker) checkAllBackends() error {
	// Get all backends from the database
	backends, err := c.db.GetAllBackends()
	if err != nil {
		return fmt.Errorf("failed to get backends: %v", err)
	}

	// Check each backend address in parallel
	var wg sync.WaitGroup
	for _, backend := range backends {
		for _, address := range backend.Addresses {
			wg.Add(1)
			go func(address models.Address) {
				defer wg.Done()
				available, err := c.checkAddress(address.IP, address.Port)

				// Only log and update if status changed
				if available != address.Available {
					var errStr string
					if err != nil {
						errStr = err.Error()
					}

					// Log the change to the database
					if err := c.db.LogAvailabilityChange(address.ID, available, errStr); err != nil {
						c.logger.Errorf("Failed to log availability change for address ID %d: %v", address.ID, err)
					}

					if available {
						c.logger.Infof("Backend %s:%d is now available", address.IP, address.Port)
					} else {
						c.logger.Warnf("Backend %s:%d is now unavailable: %v", address.IP, address.Port, err)
					}
				}
			}(address)
		}
	}

	wg.Wait()
	return nil
}

// checkAddress tests if a TCP endpoint is reachable
func (c *Checker) checkAddress(ip string, port int) (bool, error) {
	address := fmt.Sprintf("%s:%d", ip, port)
	conn, err := net.DialTimeout("tcp", address, c.checkTimeout)
	if err != nil {
		return false, err
	}
	conn.Close()
	return true, nil
}
