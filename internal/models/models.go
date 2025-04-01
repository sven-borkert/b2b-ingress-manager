package models

import (
	"net"
	"time"

	"gorm.io/gorm"
)

// Backend represents a destination server
type Backend struct {
	gorm.Model
	Name        string       `json:"name" gorm:"unique"`
	Description string       `json:"description"`
	Addresses   []Address    `json:"addresses" gorm:"foreignKey:BackendID"`
	BackendSets []BackendSet `json:"backend_sets" gorm:"many2many:backend_set_backends"`
}

// Address represents a backend server address
type Address struct {
	gorm.Model
	BackendID   uint      `json:"backend_id"`
	IP          string    `json:"ip"`
	Port        int       `json:"port"`
	Available   bool      `json:"available" gorm:"default:true"`
	LastChecked time.Time `json:"last_checked"`
}

// BackendSet represents a group of backends for load balancing
type BackendSet struct {
	gorm.Model
	Name        string    `json:"name" gorm:"unique"`
	Description string    `json:"description"`
	Backends    []Backend `json:"backends" gorm:"many2many:backend_set_backends"`
}

// SourceDefinition represents a source IP, subnet, or range
type SourceDefinition struct {
	gorm.Model
	Name        string `json:"name" gorm:"unique"`
	Description string `json:"description"`
	Type        string `json:"type" gorm:"type:varchar(10);check:type IN ('ip', 'subnet', 'range')"`
	IPAddress   string `json:"ip_address,omitempty"`
	Subnet      string `json:"subnet,omitempty"`
	RangeStart  string `json:"range_start,omitempty"`
	RangeEnd    string `json:"range_end,omitempty"`
}

// Rule represents a routing rule
type Rule struct {
	gorm.Model
	SourceDefinitionID uint             `json:"source_definition_id"`
	SourceDefinition   SourceDefinition `json:"source_definition" gorm:"foreignKey:SourceDefinitionID"`
	DestinationPort    int              `json:"destination_port"`
	Protocol           string           `json:"protocol" gorm:"type:varchar(5);check:protocol IN ('tcp', 'udp', 'all')"`
	BackendSetID       uint             `json:"backend_set_id"`
	BackendSet         BackendSet       `json:"backend_set" gorm:"foreignKey:BackendSetID"`
	Priority           int              `json:"priority" gorm:"default:0"`
	Enabled            bool             `json:"enabled" gorm:"default:true"`
}

// ConfigChange represents a log of configuration changes
type ConfigChange struct {
	gorm.Model
	ChangeType  string `json:"change_type" gorm:"type:varchar(10);check:change_type IN ('create', 'update', 'delete')"`
	EntityType  string `json:"entity_type" gorm:"type:varchar(20);check:entity_type IN ('backend', 'address', 'backend_set', 'source_definition', 'rule')"`
	EntityID    uint   `json:"entity_id"`
	Description string `json:"description"`
	ChangedBy   string `json:"changed_by"`
}

// AvailabilityLog logs backend availability status changes
type AvailabilityLog struct {
	gorm.Model
	AddressID  uint      `json:"address_id"`
	Address    Address   `json:"address" gorm:"foreignKey:AddressID"`
	Available  bool      `json:"available"`
	CheckTime  time.Time `json:"check_time"`
	CheckError string    `json:"check_error,omitempty"`
}

// Validate checks if a source definition is valid
func (s *SourceDefinition) Validate() bool {
	switch s.Type {
	case "ip":
		return net.ParseIP(s.IPAddress) != nil
	case "subnet":
		_, _, err := net.ParseCIDR(s.Subnet)
		return err == nil
	case "range":
		start := net.ParseIP(s.RangeStart)
		end := net.ParseIP(s.RangeEnd)
		return start != nil && end != nil && CompareIPs(start, end) <= 0
	default:
		return false
	}
}

// CompareIPs compares two IP addresses
func CompareIPs(ip1, ip2 net.IP) int {
	for i := 0; i < len(ip1); i++ {
		if i >= len(ip2) {
			return 1
		}
		if ip1[i] < ip2[i] {
			return -1
		}
		if ip1[i] > ip2[i] {
			return 1
		}
	}
	if len(ip1) < len(ip2) {
		return -1
	}
	return 0
}
