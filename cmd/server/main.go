package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/sven-borkert/b2b-ingress-manager/internal/api"
	"github.com/sven-borkert/b2b-ingress-manager/internal/database"
	"github.com/sven-borkert/b2b-ingress-manager/internal/health"
	"github.com/sven-borkert/b2b-ingress-manager/internal/models"
	"github.com/sven-borkert/b2b-ingress-manager/internal/nftables"

	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
)

// Config holds the application configuration
type Config struct {
	LogLevel            string        `yaml:"log_level"`
	DBHost              string        `yaml:"db_host"`
	DBPort              int           `yaml:"db_port"`
	DBUser              string        `yaml:"db_user"`
	DBPassword          string        `yaml:"db_password"`
	DBName              string        `yaml:"db_name"`
	DBSSLMode           string        `yaml:"db_sslmode"`
	APIListenAddr       string        `yaml:"api_listen"`
	UpdateInterval      time.Duration `yaml:"update_interval"`
	HealthCheckTimeout  time.Duration `yaml:"health_timeout"`
	HealthCheckInterval time.Duration `yaml:"health_interval"`
	NFTablesTable       string        `yaml:"nft_table"`
	NFTablesChain       string        `yaml:"nft_chain"`
}

// defaultConfig returns the default configuration
func defaultConfig() Config {
	return Config{
		LogLevel:            "info",
		DBHost:              "localhost",
		DBPort:              5432,
		DBUser:              "postgres",
		DBPassword:          "",
		DBName:              "nftables",
		DBSSLMode:           "disable",
		APIListenAddr:       ":8080",
		UpdateInterval:      30 * time.Second,
		HealthCheckTimeout:  5 * time.Second,
		HealthCheckInterval: 60 * time.Second,
		NFTablesTable:       "nat",
		NFTablesChain:       "prerouting",
	}
}

// validateConfig checks if all required fields are present
func validateConfig(config Config) error {
	// Check required fields
	if config.DBHost == "" {
		return fmt.Errorf("missing required parameter: db_host")
	}
	if config.DBPort == 0 {
		return fmt.Errorf("missing required parameter: db_port")
	}
	if config.DBUser == "" {
		return fmt.Errorf("missing required parameter: db_user")
	}
	if config.DBName == "" {
		return fmt.Errorf("missing required parameter: db_name")
	}
	if config.APIListenAddr == "" {
		return fmt.Errorf("missing required parameter: api_listen")
	}
	if config.NFTablesTable == "" {
		return fmt.Errorf("missing required parameter: nft_table")
	}
	if config.NFTablesChain == "" {
		return fmt.Errorf("missing required parameter: nft_chain")
	}
	return nil
}

func main() {
	// Parse command-line flags
	config := parseConfig()

	// Initialize logger
	logger := setupLogger(config.LogLevel)
	logger.Info("Starting nftables manager")

	// Connect to the database
	db, err := setupDatabase(config, logger)
	if err != nil {
		logger.Fatalf("Failed to connect to database: %v", err)
	}

	// Initialize nftables manager
	nft, err := setupNFTablesManager(config, logger)
	if err != nil {
		logger.Fatalf("Failed to initialize nftables manager: %v", err)
	}
	defer nft.Cleanup()

	// Initialize the health checker
	healthChecker := setupHealthChecker(config, db, logger)

	// Start health checker
	healthChecker.Start()
	defer healthChecker.Stop()

	// Initialize API server
	apiServer := setupAPIServer(config, db, logger)

	// Create a wait group to manage goroutines
	var wg sync.WaitGroup

	// Handle graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Create a context for shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start API server
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := apiServer.Start(api.Config{ListenAddr: config.APIListenAddr}); err != nil && err != http.ErrServerClosed {
			logger.Fatalf("API server failed: %v", err)
		}
	}()

	// Start config updater
	wg.Add(1)
	go func() {
		defer wg.Done()
		runConfigUpdater(ctx, config, db, nft, logger)
	}()

	<-sigCh
	logger.Info("Received shutdown signal, gracefully shutting down...")

	// Cancel context to stop background tasks
	cancel()

	// Create a context with timeout for API server shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	// Shutdown API server
	if err := apiServer.Shutdown(shutdownCtx); err != nil {
		logger.Errorf("Error shutting down API server: %v", err)
	}

	// Wait for goroutines to finish
	wg.Wait()
	logger.Info("Shutdown complete")
}

// parseConfig loads the configuration from file and command-line flags
func parseConfig() Config {
	// Start with default configuration
	config := defaultConfig()

	// Define command-line flags
	configFile := flag.String("config", "config.yaml", "Path to configuration file")

	// Define flags to override config file options
	logLevel := flag.String("log-level", "", "Log level (debug, info, warn, error)")
	dbHost := flag.String("db-host", "", "PostgreSQL host")
	dbPort := flag.Int("db-port", 0, "PostgreSQL port")
	dbUser := flag.String("db-user", "", "PostgreSQL user")
	dbPassword := flag.String("db-password", "", "PostgreSQL password")
	dbName := flag.String("db-name", "", "PostgreSQL database name")
	dbSSLMode := flag.String("db-sslmode", "", "PostgreSQL SSL mode")
	apiListenAddr := flag.String("api-listen", "", "API server listen address")
	updateInterval := flag.Duration("update-interval", 0, "NFTables update interval")
	healthCheckTimeout := flag.Duration("health-timeout", 0, "Health check timeout")
	healthCheckInterval := flag.Duration("health-interval", 0, "Health check interval")
	nftTable := flag.String("nft-table", "", "NFTables table name")
	nftChain := flag.String("nft-chain", "", "NFTables chain name")

	flag.Parse()

	// Try to load configuration from file
	if _, err := os.Stat(*configFile); err == nil {
		fileData, err := os.ReadFile(*configFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading configuration file: %v\n", err)
			os.Exit(1)
		}

		if err := yaml.Unmarshal(fileData, &config); err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing configuration file: %v\n", err)
			os.Exit(1)
		}
	} else if *configFile != "config.yaml" {
		// Only complain if user explicitly specified a config file
		fmt.Fprintf(os.Stderr, "Warning: Configuration file %s not found\n", *configFile)
	}

	// Override config with command-line flags if provided
	if *logLevel != "" {
		config.LogLevel = *logLevel
	}
	if *dbHost != "" {
		config.DBHost = *dbHost
	}
	if *dbPort != 0 {
		config.DBPort = *dbPort
	}
	if *dbUser != "" {
		config.DBUser = *dbUser
	}
	if *dbPassword != "" {
		config.DBPassword = *dbPassword
	}
	if *dbName != "" {
		config.DBName = *dbName
	}
	if *dbSSLMode != "" {
		config.DBSSLMode = *dbSSLMode
	}
	if *apiListenAddr != "" {
		config.APIListenAddr = *apiListenAddr
	}
	if *updateInterval != 0 {
		config.UpdateInterval = *updateInterval
	}
	if *healthCheckTimeout != 0 {
		config.HealthCheckTimeout = *healthCheckTimeout
	}
	if *healthCheckInterval != 0 {
		config.HealthCheckInterval = *healthCheckInterval
	}
	if *nftTable != "" {
		config.NFTablesTable = *nftTable
	}
	if *nftChain != "" {
		config.NFTablesChain = *nftChain
	}

	// Validate the configuration
	if err := validateConfig(config); err != nil {
		fmt.Fprintf(os.Stderr, "Configuration error: %v\n", err)
		os.Exit(1)
	}

	return config
}

// setupLogger initializes the logger
func setupLogger(level string) *logrus.Logger {
	logger := logrus.New()
	logger.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
	})

	switch level {
	case "debug":
		logger.SetLevel(logrus.DebugLevel)
	case "info":
		logger.SetLevel(logrus.InfoLevel)
	case "warn":
		logger.SetLevel(logrus.WarnLevel)
	case "error":
		logger.SetLevel(logrus.ErrorLevel)
	default:
		logger.SetLevel(logrus.InfoLevel)
	}

	return logger
}

// setupDatabase initializes the database connection
func setupDatabase(config Config, logger *logrus.Logger) (*database.Service, error) {
	dbConfig := database.Config{
		Host:     config.DBHost,
		Port:     config.DBPort,
		User:     config.DBUser,
		Password: config.DBPassword,
		DBName:   config.DBName,
		SSLMode:  config.DBSSLMode,
	}

	return database.NewService(dbConfig, logger)
}

// setupNFTablesManager initializes the nftables manager
func setupNFTablesManager(config Config, logger *logrus.Logger) (*nftables.Manager, error) {
	nftConfig := nftables.Config{
		TableName: config.NFTablesTable,
		ChainName: config.NFTablesChain,
	}

	nft, err := nftables.NewManager(nftConfig, logger)
	if err != nil {
		return nil, err
	}

	if err := nft.Initialize(); err != nil {
		return nil, err
	}

	return nft, nil
}

// setupHealthChecker initializes the health checker
func setupHealthChecker(config Config, db *database.Service, logger *logrus.Logger) *health.Checker {
	healthConfig := health.Config{
		CheckTimeout: config.HealthCheckTimeout,
		Interval:     config.HealthCheckInterval,
	}

	return health.NewChecker(db, healthConfig, logger)
}

// setupAPIServer initializes the API server
func setupAPIServer(config Config, db *database.Service, logger *logrus.Logger) *api.Server {
	apiConfig := api.Config{
		ListenAddr: config.APIListenAddr,
	}

	return api.NewServer(db, apiConfig, logger)
}

// runConfigUpdater periodically updates nftables configuration
func runConfigUpdater(ctx context.Context, config Config, db *database.Service, nft *nftables.Manager, logger *logrus.Logger) {
	ticker := time.NewTicker(config.UpdateInterval)
	defer ticker.Stop()

	// Run the updater immediately on startup
	if err := updateNFTables(db, nft, logger); err != nil {
		logger.Errorf("Failed to update nftables: %v", err)
	}

	for {
		select {
		case <-ticker.C:
			if err := updateNFTables(db, nft, logger); err != nil {
				logger.Errorf("Failed to update nftables: %v", err)
			}
		case <-ctx.Done():
			logger.Info("Stopping config updater...")
			return
		}
	}
}

// updateNFTables applies the current configuration to nftables
func updateNFTables(db *database.Service, nft *nftables.Manager, logger *logrus.Logger) error {
	// Get active rules from the database
	rules, err := db.GetActiveRules()
	if err != nil {
		return fmt.Errorf("failed to get active rules: %v", err)
	}

	logger.Debugf("Got %d active rules from database", len(rules))

	// Get available backend addresses for each backend set
	backendAddresses := make(map[uint][]models.Address)
	for _, rule := range rules {
		addresses, err := db.GetAvailableBackendAddresses(rule.BackendSetID)
		if err != nil {
			logger.Errorf("Failed to get addresses for backend set %d: %v", rule.BackendSetID, err)
			continue
		}

		backendAddresses[rule.BackendSetID] = addresses
	}

	// Apply the rules to nftables
	return nft.ApplyRules(rules, backendAddresses)
}
