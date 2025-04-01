package api

import (
	"context"
	"net"
	"net/http"
	"strconv"

	"github.com/sven-borkert/b2b-ingress-manager/internal/database"
	"github.com/sven-borkert/b2b-ingress-manager/internal/models"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

// Server represents the API server
type Server struct {
	router *gin.Engine
	db     *database.Service
	logger *logrus.Logger
	srv    *http.Server
}

// Config for the API server
type Config struct {
	ListenAddr string
}

// NewServer creates a new API server
func NewServer(db *database.Service, config Config, logger *logrus.Logger) *Server {
	router := gin.New()
	router.Use(gin.Recovery())

	// Add a logger middleware
	router.Use(func(c *gin.Context) {
		logger.WithFields(logrus.Fields{
			"method": c.Request.Method,
			"path":   c.Request.URL.Path,
			"ip":     c.ClientIP(),
		}).Info("API request")
		c.Next()
	})

	server := &Server{
		router: router,
		db:     db,
		logger: logger,
	}

	// Register routes
	server.registerRoutes()

	return server
}

// Start begins serving API requests
func (s *Server) Start(config Config) error {
	s.logger.Infof("Starting API server on %s", config.ListenAddr)
	s.srv = &http.Server{
		Addr:    config.ListenAddr,
		Handler: s.router,
	}
	return s.srv.ListenAndServe()
}

// Shutdown gracefully shuts down the API server
func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("Shutting down API server...")
	if s.srv != nil {
		return s.srv.Shutdown(ctx)
	}
	return nil
}

// registerRoutes sets up all API routes
func (s *Server) registerRoutes() {
	api := s.router.Group("/api")
	{
		// Backend routes
		api.GET("/backends", s.getBackends)
		api.GET("/backends/:id", s.getBackend)
		api.POST("/backends", s.createBackend)
		api.PUT("/backends/:id", s.updateBackend)
		api.DELETE("/backends/:id", s.deleteBackend)

		// Backend address routes
		api.POST("/backends/:id/addresses", s.addBackendAddress)
		api.PUT("/addresses/:id", s.updateAddress)
		api.DELETE("/addresses/:id", s.deleteAddress)

		// Backend set routes
		api.GET("/backend-sets", s.getBackendSets)
		api.GET("/backend-sets/:id", s.getBackendSet)
		api.POST("/backend-sets", s.createBackendSet)
		api.PUT("/backend-sets/:id", s.updateBackendSet)
		api.DELETE("/backend-sets/:id", s.deleteBackendSet)

		// Source definition routes
		api.GET("/source-definitions", s.getSourceDefinitions)
		api.GET("/source-definitions/:id", s.getSourceDefinition)
		api.POST("/source-definitions", s.createSourceDefinition)
		api.PUT("/source-definitions/:id", s.updateSourceDefinition)
		api.DELETE("/source-definitions/:id", s.deleteSourceDefinition)

		// Rule routes
		api.GET("/rules", s.getRules)
		api.GET("/rules/:id", s.getRule)
		api.POST("/rules", s.createRule)
		api.PUT("/rules/:id", s.updateRule)
		api.DELETE("/rules/:id", s.deleteRule)

		// Logs routes
		api.GET("/logs/config", s.getConfigLogs)
		api.GET("/logs/availability", s.getAvailabilityLogs)
	}
}

// Backend route handlers
func (s *Server) getBackends(c *gin.Context) {
	backends, err := s.db.GetAllBackends()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, backends)
}

func (s *Server) getBackend(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID format"})
		return
	}

	backend, err := s.db.GetBackend(uint(id))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Backend not found"})
		return
	}

	c.JSON(http.StatusOK, backend)
}

func (s *Server) createBackend(c *gin.Context) {
	var backend models.Backend
	if err := c.ShouldBindJSON(&backend); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := s.db.CreateBackend(&backend, c.ClientIP()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, backend)
}

func (s *Server) updateBackend(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID format"})
		return
	}

	var backend models.Backend
	if err := c.ShouldBindJSON(&backend); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	backend.ID = uint(id)
	if err := s.db.UpdateBackend(&backend, c.ClientIP()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, backend)
}

func (s *Server) deleteBackend(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID format"})
		return
	}

	if err := s.db.DeleteBackend(uint(id), c.ClientIP()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.Status(http.StatusNoContent)
}

// Placeholder implementations for other API handlers
// These would be implemented similarly to the backend handlers

func (s *Server) addBackendAddress(c *gin.Context) {
	backendID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid backend ID format"})
		return
	}

	// Check if the backend exists
	_, err = s.db.GetBackend(uint(backendID))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Backend not found"})
		return
	}

	var address models.Address
	if err := c.ShouldBindJSON(&address); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate the IP address
	if net.ParseIP(address.IP) == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid IP address"})
		return
	}

	if err := s.db.CreateAddress(uint(backendID), &address, c.ClientIP()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, address)
}

func (s *Server) updateAddress(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID format"})
		return
	}

	// Check if the address exists
	existingAddress, err := s.db.GetAddress(uint(id))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Address not found"})
		return
	}

	var address models.Address
	if err := c.ShouldBindJSON(&address); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Keep the original backend ID
	address.ID = uint(id)
	address.BackendID = existingAddress.BackendID

	// Validate the IP address
	if net.ParseIP(address.IP) == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid IP address"})
		return
	}

	if err := s.db.UpdateAddress(&address, c.ClientIP()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, address)
}

func (s *Server) deleteAddress(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID format"})
		return
	}

	if err := s.db.DeleteAddress(uint(id), c.ClientIP()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.Status(http.StatusNoContent)
}

func (s *Server) getBackendSets(c *gin.Context) {
	backendSets, err := s.db.GetAllBackendSets()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, backendSets)
}

func (s *Server) getBackendSet(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID format"})
		return
	}

	backendSet, err := s.db.GetBackendSet(uint(id))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Backend set not found"})
		return
	}

	c.JSON(http.StatusOK, backendSet)
}

func (s *Server) createBackendSet(c *gin.Context) {
	var backendSet models.BackendSet
	if err := c.ShouldBindJSON(&backendSet); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := s.db.CreateBackendSet(&backendSet, c.ClientIP()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, backendSet)
}

func (s *Server) updateBackendSet(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID format"})
		return
	}

	var backendSet models.BackendSet
	if err := c.ShouldBindJSON(&backendSet); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	backendSet.ID = uint(id)
	if err := s.db.UpdateBackendSet(&backendSet, c.ClientIP()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, backendSet)
}

func (s *Server) deleteBackendSet(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID format"})
		return
	}

	if err := s.db.DeleteBackendSet(uint(id), c.ClientIP()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.Status(http.StatusNoContent)
}

func (s *Server) getSourceDefinitions(c *gin.Context) {
	sourceDefinitions, err := s.db.GetAllSourceDefinitions()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, sourceDefinitions)
}

func (s *Server) getSourceDefinition(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID format"})
		return
	}

	sourceDefinition, err := s.db.GetSourceDefinition(uint(id))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Source definition not found"})
		return
	}

	c.JSON(http.StatusOK, sourceDefinition)
}

func (s *Server) createSourceDefinition(c *gin.Context) {
	var sourceDefinition models.SourceDefinition
	if err := c.ShouldBindJSON(&sourceDefinition); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate the source definition
	if !sourceDefinition.Validate() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid source definition parameters"})
		return
	}

	if err := s.db.CreateSourceDefinition(&sourceDefinition, c.ClientIP()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, sourceDefinition)
}

func (s *Server) updateSourceDefinition(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID format"})
		return
	}

	var sourceDefinition models.SourceDefinition
	if err := c.ShouldBindJSON(&sourceDefinition); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	sourceDefinition.ID = uint(id)

	// Validate the source definition
	if !sourceDefinition.Validate() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid source definition parameters"})
		return
	}

	if err := s.db.UpdateSourceDefinition(&sourceDefinition, c.ClientIP()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, sourceDefinition)
}

func (s *Server) deleteSourceDefinition(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID format"})
		return
	}

	if err := s.db.DeleteSourceDefinition(uint(id), c.ClientIP()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.Status(http.StatusNoContent)
}

func (s *Server) getRules(c *gin.Context) {
	rules, err := s.db.GetAllRules()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, rules)
}

func (s *Server) getRule(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID format"})
		return
	}

	rule, err := s.db.GetRule(uint(id))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Rule not found"})
		return
	}

	c.JSON(http.StatusOK, rule)
}

func (s *Server) createRule(c *gin.Context) {
	var rule models.Rule
	if err := c.ShouldBindJSON(&rule); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate the rule
	if rule.SourceDefinitionID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Source definition ID is required"})
		return
	}
	if rule.BackendSetID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Backend set ID is required"})
		return
	}

	if err := s.db.CreateRule(&rule, c.ClientIP()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, rule)
}

func (s *Server) updateRule(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID format"})
		return
	}

	var rule models.Rule
	if err := c.ShouldBindJSON(&rule); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	rule.ID = uint(id)

	// Validate the rule
	if rule.SourceDefinitionID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Source definition ID is required"})
		return
	}
	if rule.BackendSetID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Backend set ID is required"})
		return
	}

	if err := s.db.UpdateRule(&rule, c.ClientIP()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, rule)
}

func (s *Server) deleteRule(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID format"})
		return
	}

	if err := s.db.DeleteRule(uint(id), c.ClientIP()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.Status(http.StatusNoContent)
}

func (s *Server) getConfigLogs(c *gin.Context) {
	limit := 100
	offset := 0

	// Parse pagination parameters if provided
	limitParam := c.Query("limit")
	offsetParam := c.Query("offset")

	if limitParam != "" {
		if parsedLimit, err := strconv.Atoi(limitParam); err == nil && parsedLimit > 0 {
			limit = parsedLimit
		}
	}

	if offsetParam != "" {
		if parsedOffset, err := strconv.Atoi(offsetParam); err == nil && parsedOffset >= 0 {
			offset = parsedOffset
		}
	}

	logs, err := s.db.GetConfigChangeLogs(limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, logs)
}

func (s *Server) getAvailabilityLogs(c *gin.Context) {
	limit := 100
	offset := 0

	// Parse pagination parameters if provided
	limitParam := c.Query("limit")
	offsetParam := c.Query("offset")

	if limitParam != "" {
		if parsedLimit, err := strconv.Atoi(limitParam); err == nil && parsedLimit > 0 {
			limit = parsedLimit
		}
	}

	if offsetParam != "" {
		if parsedOffset, err := strconv.Atoi(offsetParam); err == nil && parsedOffset >= 0 {
			offset = parsedOffset
		}
	}

	logs, err := s.db.GetAvailabilityLogs(limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, logs)
}
