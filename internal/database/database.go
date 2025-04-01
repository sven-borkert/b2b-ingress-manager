package database

import (
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/sven-borkert/b2b-ingress-manager/internal/models"

	"github.com/sirupsen/logrus"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// Config holds database connection configuration
type Config struct {
	Host     string
	Port     int
	User     string
	Password string
	DBName   string
	SSLMode  string
}

// Service provides database operations
type Service struct {
	db     *gorm.DB
	logger *logrus.Logger
	mu     sync.RWMutex
}

// NewService creates a new database service
func NewService(config Config, logger *logrus.Logger) (*Service, error) {
	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		config.Host, config.Port, config.User, config.Password, config.DBName, config.SSLMode)

	// Set up a simple logger for GORM that outputs to stdout
	newLogger := gormlogger.New(
		log.New(os.Stdout, "\r\n", log.LstdFlags),
		gormlogger.Config{
			SlowThreshold:             time.Second,
			LogLevel:                  gormlogger.Silent, // Only log errors
			IgnoreRecordNotFoundError: true,
			Colorful:                  false,
		},
	)

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: newLogger,
	})
	if err != nil {
		return nil, err
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}

	// Set connection pool settings
	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetMaxOpenConns(100)
	sqlDB.SetConnMaxLifetime(time.Hour)

	service := &Service{
		db:     db,
		logger: logger,
	}

	// Create tables if they don't exist
	if err := service.migrateSchema(); err != nil {
		return nil, err
	}

	return service, nil
}

// migrateSchema creates database tables if they don't exist
func (s *Service) migrateSchema() error {
	// Using GORM AutoMigrate to create or update tables based on struct models
	return s.db.AutoMigrate(
		&models.Backend{},
		&models.Address{},
		&models.BackendSet{},
		&models.SourceDefinition{},
		&models.Rule{},
		&models.ConfigChange{},
		&models.AvailabilityLog{},
	)
}

// LogConfigChange records a configuration change to the database
func (s *Service) LogConfigChange(changeType, entityType string, entityID uint, description, changedBy string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	change := models.ConfigChange{
		ChangeType:  changeType,
		EntityType:  entityType,
		EntityID:    entityID,
		Description: description,
		ChangedBy:   changedBy,
	}

	return s.db.Create(&change).Error
}

// LogAvailabilityChange records a backend availability change
func (s *Service) LogAvailabilityChange(addressID uint, available bool, checkError string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	log := models.AvailabilityLog{
		AddressID:  addressID,
		Available:  available,
		CheckTime:  time.Now(),
		CheckError: checkError,
	}

	// Also update the address availability status
	err := s.db.Model(&models.Address{}).Where("id = ?", addressID).Updates(map[string]interface{}{
		"available":    available,
		"last_checked": time.Now(),
	}).Error

	if err != nil {
		return err
	}

	return s.db.Create(&log).Error
}

// GetAllBackends retrieves all backends from the database
func (s *Service) GetAllBackends() ([]models.Backend, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var backends []models.Backend
	err := s.db.Preload("Addresses").Find(&backends).Error
	return backends, err
}

// GetBackend retrieves a backend by ID
func (s *Service) GetBackend(id uint) (*models.Backend, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var backend models.Backend
	err := s.db.Preload("Addresses").First(&backend, id).Error
	if err != nil {
		return nil, err
	}
	return &backend, nil
}

// CreateBackend creates a new backend
func (s *Service) CreateBackend(backend *models.Backend, changedBy string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx := s.db.Begin()
	if err := tx.Create(backend).Error; err != nil {
		tx.Rollback()
		return err
	}

	// Log the change
	if err := tx.Create(&models.ConfigChange{
		ChangeType:  "create",
		EntityType:  "backend",
		EntityID:    backend.ID,
		Description: fmt.Sprintf("Created backend %s", backend.Name),
		ChangedBy:   changedBy,
	}).Error; err != nil {
		tx.Rollback()
		return err
	}

	return tx.Commit().Error
}

// UpdateBackend updates an existing backend
func (s *Service) UpdateBackend(backend *models.Backend, changedBy string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx := s.db.Begin()
	if err := tx.Save(backend).Error; err != nil {
		tx.Rollback()
		return err
	}

	// Log the change
	if err := tx.Create(&models.ConfigChange{
		ChangeType:  "update",
		EntityType:  "backend",
		EntityID:    backend.ID,
		Description: fmt.Sprintf("Updated backend %s", backend.Name),
		ChangedBy:   changedBy,
	}).Error; err != nil {
		tx.Rollback()
		return err
	}

	return tx.Commit().Error
}

// DeleteBackend deletes a backend by ID
func (s *Service) DeleteBackend(id uint, changedBy string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var backend models.Backend
	if err := s.db.First(&backend, id).Error; err != nil {
		return err
	}

	tx := s.db.Begin()

	// First remove the backend from any backend sets
	if err := tx.Exec("DELETE FROM backend_set_backends WHERE backend_id = ?", id).Error; err != nil {
		tx.Rollback()
		return err
	}

	// Delete associated addresses
	if err := tx.Delete(&models.Address{}, "backend_id = ?", id).Error; err != nil {
		tx.Rollback()
		return err
	}

	// Delete the backend
	if err := tx.Delete(&backend).Error; err != nil {
		tx.Rollback()
		return err
	}

	// Log the change
	if err := tx.Create(&models.ConfigChange{
		ChangeType:  "delete",
		EntityType:  "backend",
		EntityID:    id,
		Description: fmt.Sprintf("Deleted backend %s", backend.Name),
		ChangedBy:   changedBy,
	}).Error; err != nil {
		tx.Rollback()
		return err
	}

	return tx.Commit().Error
}

// GetAllRules retrieves all rules with their related entities
func (s *Service) GetAllRules() ([]models.Rule, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var rules []models.Rule
	err := s.db.Preload("SourceDefinition").Preload("BackendSet").Preload("BackendSet.Backends").Find(&rules).Error
	return rules, err
}

// GetActiveRules retrieves only enabled rules with their related entities
func (s *Service) GetActiveRules() ([]models.Rule, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var rules []models.Rule
	err := s.db.Preload("SourceDefinition").Preload("BackendSet").Preload("BackendSet.Backends").
		Preload("BackendSet.Backends.Addresses", "available = ?", true).
		Where("enabled = ?", true).
		Order("priority DESC").
		Find(&rules).Error
	return rules, err
}

// GetAvailableBackendAddresses gets all available addresses for a given backend set
func (s *Service) GetAvailableBackendAddresses(backendSetID uint) ([]models.Address, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var addresses []models.Address
	err := s.db.Raw(`
		SELECT a.* FROM addresses a
		JOIN backends b ON a.backend_id = b.id
		JOIN backend_set_backends bsb ON b.id = bsb.backend_id
		WHERE bsb.backend_set_id = ? AND a.available = true
	`, backendSetID).Scan(&addresses).Error

	return addresses, err
}

// CreateAddress adds a new address to a backend
func (s *Service) CreateAddress(backendID uint, address *models.Address, changedBy string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Set backend ID
	address.BackendID = backendID

	tx := s.db.Begin()
	if err := tx.Create(address).Error; err != nil {
		tx.Rollback()
		return err
	}

	// Log the change
	if err := tx.Create(&models.ConfigChange{
		ChangeType:  "create",
		EntityType:  "address",
		EntityID:    address.ID,
		Description: fmt.Sprintf("Added address %s:%d to backend ID %d", address.IP, address.Port, backendID),
		ChangedBy:   changedBy,
	}).Error; err != nil {
		tx.Rollback()
		return err
	}

	return tx.Commit().Error
}

// GetAddress retrieves an address by ID
func (s *Service) GetAddress(id uint) (*models.Address, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var address models.Address
	err := s.db.First(&address, id).Error
	if err != nil {
		return nil, err
	}
	return &address, nil
}

// UpdateAddress updates an existing address
func (s *Service) UpdateAddress(address *models.Address, changedBy string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx := s.db.Begin()
	if err := tx.Save(address).Error; err != nil {
		tx.Rollback()
		return err
	}

	// Log the change
	if err := tx.Create(&models.ConfigChange{
		ChangeType:  "update",
		EntityType:  "address",
		EntityID:    address.ID,
		Description: fmt.Sprintf("Updated address %s:%d", address.IP, address.Port),
		ChangedBy:   changedBy,
	}).Error; err != nil {
		tx.Rollback()
		return err
	}

	return tx.Commit().Error
}

// DeleteAddress deletes an address by ID
func (s *Service) DeleteAddress(id uint, changedBy string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var address models.Address
	if err := s.db.First(&address, id).Error; err != nil {
		return err
	}

	tx := s.db.Begin()
	if err := tx.Delete(&address).Error; err != nil {
		tx.Rollback()
		return err
	}

	// Log the change
	if err := tx.Create(&models.ConfigChange{
		ChangeType:  "delete",
		EntityType:  "address",
		EntityID:    id,
		Description: fmt.Sprintf("Deleted address %s:%d", address.IP, address.Port),
		ChangedBy:   changedBy,
	}).Error; err != nil {
		tx.Rollback()
		return err
	}

	return tx.Commit().Error
}

// GetAllBackendSets retrieves all backend sets from the database
func (s *Service) GetAllBackendSets() ([]models.BackendSet, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var backendSets []models.BackendSet
	err := s.db.Preload("Backends").Find(&backendSets).Error
	return backendSets, err
}

// GetBackendSet retrieves a backend set by ID
func (s *Service) GetBackendSet(id uint) (*models.BackendSet, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var backendSet models.BackendSet
	err := s.db.Preload("Backends").First(&backendSet, id).Error
	if err != nil {
		return nil, err
	}
	return &backendSet, nil
}

// CreateBackendSet creates a new backend set
func (s *Service) CreateBackendSet(backendSet *models.BackendSet, changedBy string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx := s.db.Begin()

	// First create the backend set
	if err := tx.Create(backendSet).Error; err != nil {
		tx.Rollback()
		return err
	}

	// Then associate the backends
	if len(backendSet.Backends) > 0 {
		if err := tx.Model(backendSet).Association("Backends").Replace(backendSet.Backends); err != nil {
			tx.Rollback()
			return err
		}
	}

	// Log the change
	if err := tx.Create(&models.ConfigChange{
		ChangeType:  "create",
		EntityType:  "backend_set",
		EntityID:    backendSet.ID,
		Description: fmt.Sprintf("Created backend set %s", backendSet.Name),
		ChangedBy:   changedBy,
	}).Error; err != nil {
		tx.Rollback()
		return err
	}

	return tx.Commit().Error
}

// UpdateBackendSet updates an existing backend set
func (s *Service) UpdateBackendSet(backendSet *models.BackendSet, changedBy string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx := s.db.Begin()

	// Clear existing backends associations and re-add them
	if err := tx.Model(backendSet).Association("Backends").Clear(); err != nil {
		tx.Rollback()
		return err
	}

	if err := tx.Save(backendSet).Error; err != nil {
		tx.Rollback()
		return err
	}

	// Log the change
	if err := tx.Create(&models.ConfigChange{
		ChangeType:  "update",
		EntityType:  "backend_set",
		EntityID:    backendSet.ID,
		Description: fmt.Sprintf("Updated backend set %s", backendSet.Name),
		ChangedBy:   changedBy,
	}).Error; err != nil {
		tx.Rollback()
		return err
	}

	return tx.Commit().Error
}

// DeleteBackendSet deletes a backend set by ID
func (s *Service) DeleteBackendSet(id uint, changedBy string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var backendSet models.BackendSet
	if err := s.db.First(&backendSet, id).Error; err != nil {
		return err
	}

	// Check if there are any rules using this backend set
	var count int64
	if err := s.db.Model(&models.Rule{}).Where("backend_set_id = ?", id).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return fmt.Errorf("cannot delete backend set: it is used by %d rules", count)
	}

	tx := s.db.Begin()

	// Remove associations with backends
	if err := tx.Model(&backendSet).Association("Backends").Clear(); err != nil {
		tx.Rollback()
		return err
	}

	// Delete the backend set
	if err := tx.Delete(&backendSet).Error; err != nil {
		tx.Rollback()
		return err
	}

	// Log the change
	if err := tx.Create(&models.ConfigChange{
		ChangeType:  "delete",
		EntityType:  "backend_set",
		EntityID:    id,
		Description: fmt.Sprintf("Deleted backend set %s", backendSet.Name),
		ChangedBy:   changedBy,
	}).Error; err != nil {
		tx.Rollback()
		return err
	}

	return tx.Commit().Error
}

// GetAllSourceDefinitions retrieves all source definitions from the database
func (s *Service) GetAllSourceDefinitions() ([]models.SourceDefinition, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var sourceDefinitions []models.SourceDefinition
	err := s.db.Find(&sourceDefinitions).Error
	return sourceDefinitions, err
}

// GetSourceDefinition retrieves a source definition by ID
func (s *Service) GetSourceDefinition(id uint) (*models.SourceDefinition, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var sourceDefinition models.SourceDefinition
	err := s.db.First(&sourceDefinition, id).Error
	if err != nil {
		return nil, err
	}
	return &sourceDefinition, nil
}

// CreateSourceDefinition creates a new source definition
func (s *Service) CreateSourceDefinition(sourceDefinition *models.SourceDefinition, changedBy string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Validate the source definition
	if !sourceDefinition.Validate() {
		return fmt.Errorf("invalid source definition parameters")
	}

	tx := s.db.Begin()
	if err := tx.Create(sourceDefinition).Error; err != nil {
		tx.Rollback()
		return err
	}

	// Log the change
	if err := tx.Create(&models.ConfigChange{
		ChangeType:  "create",
		EntityType:  "source_definition",
		EntityID:    sourceDefinition.ID,
		Description: fmt.Sprintf("Created source definition %s", sourceDefinition.Name),
		ChangedBy:   changedBy,
	}).Error; err != nil {
		tx.Rollback()
		return err
	}

	return tx.Commit().Error
}

// UpdateSourceDefinition updates an existing source definition
func (s *Service) UpdateSourceDefinition(sourceDefinition *models.SourceDefinition, changedBy string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Validate the source definition
	if !sourceDefinition.Validate() {
		return fmt.Errorf("invalid source definition parameters")
	}

	tx := s.db.Begin()
	if err := tx.Save(sourceDefinition).Error; err != nil {
		tx.Rollback()
		return err
	}

	// Log the change
	if err := tx.Create(&models.ConfigChange{
		ChangeType:  "update",
		EntityType:  "source_definition",
		EntityID:    sourceDefinition.ID,
		Description: fmt.Sprintf("Updated source definition %s", sourceDefinition.Name),
		ChangedBy:   changedBy,
	}).Error; err != nil {
		tx.Rollback()
		return err
	}

	return tx.Commit().Error
}

// DeleteSourceDefinition deletes a source definition by ID
func (s *Service) DeleteSourceDefinition(id uint, changedBy string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var sourceDefinition models.SourceDefinition
	if err := s.db.First(&sourceDefinition, id).Error; err != nil {
		return err
	}

	// Check if there are any rules using this source definition
	var count int64
	if err := s.db.Model(&models.Rule{}).Where("source_definition_id = ?", id).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return fmt.Errorf("cannot delete source definition: it is used by %d rules", count)
	}

	tx := s.db.Begin()
	if err := tx.Delete(&sourceDefinition).Error; err != nil {
		tx.Rollback()
		return err
	}

	// Log the change
	if err := tx.Create(&models.ConfigChange{
		ChangeType:  "delete",
		EntityType:  "source_definition",
		EntityID:    id,
		Description: fmt.Sprintf("Deleted source definition %s", sourceDefinition.Name),
		ChangedBy:   changedBy,
	}).Error; err != nil {
		tx.Rollback()
		return err
	}

	return tx.Commit().Error
}

// GetRule retrieves a rule by ID
func (s *Service) GetRule(id uint) (*models.Rule, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var rule models.Rule
	err := s.db.Preload("SourceDefinition").Preload("BackendSet").First(&rule, id).Error
	if err != nil {
		return nil, err
	}
	return &rule, nil
}

// CreateRule creates a new rule
func (s *Service) CreateRule(rule *models.Rule, changedBy string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx := s.db.Begin()
	if err := tx.Create(rule).Error; err != nil {
		tx.Rollback()
		return err
	}

	// Log the change
	if err := tx.Create(&models.ConfigChange{
		ChangeType:  "create",
		EntityType:  "rule",
		EntityID:    rule.ID,
		Description: fmt.Sprintf("Created rule with priority %d", rule.Priority),
		ChangedBy:   changedBy,
	}).Error; err != nil {
		tx.Rollback()
		return err
	}

	return tx.Commit().Error
}

// UpdateRule updates an existing rule
func (s *Service) UpdateRule(rule *models.Rule, changedBy string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx := s.db.Begin()
	if err := tx.Save(rule).Error; err != nil {
		tx.Rollback()
		return err
	}

	// Log the change
	if err := tx.Create(&models.ConfigChange{
		ChangeType:  "update",
		EntityType:  "rule",
		EntityID:    rule.ID,
		Description: fmt.Sprintf("Updated rule with priority %d", rule.Priority),
		ChangedBy:   changedBy,
	}).Error; err != nil {
		tx.Rollback()
		return err
	}

	return tx.Commit().Error
}

// DeleteRule deletes a rule by ID
func (s *Service) DeleteRule(id uint, changedBy string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var rule models.Rule
	if err := s.db.First(&rule, id).Error; err != nil {
		return err
	}

	tx := s.db.Begin()
	if err := tx.Delete(&rule).Error; err != nil {
		tx.Rollback()
		return err
	}

	// Log the change
	if err := tx.Create(&models.ConfigChange{
		ChangeType:  "delete",
		EntityType:  "rule",
		EntityID:    id,
		Description: fmt.Sprintf("Deleted rule with priority %d", rule.Priority),
		ChangedBy:   changedBy,
	}).Error; err != nil {
		tx.Rollback()
		return err
	}

	return tx.Commit().Error
}

// GetConfigChangeLogs retrieves configuration change logs
func (s *Service) GetConfigChangeLogs(limit, offset int) ([]models.ConfigChange, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var logs []models.ConfigChange
	err := s.db.Order("created_at DESC").Limit(limit).Offset(offset).Find(&logs).Error
	return logs, err
}

// GetAvailabilityLogs retrieves backend availability logs
func (s *Service) GetAvailabilityLogs(limit, offset int) ([]models.AvailabilityLog, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var logs []models.AvailabilityLog
	err := s.db.Preload("Address").Order("created_at DESC").Limit(limit).Offset(offset).Find(&logs).Error
	return logs, err
}
