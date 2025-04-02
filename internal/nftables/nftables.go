package nftables

import (
	"fmt"
	"math/rand"
	"net"
	"sync"
	"time"

	"github.com/sven-borkert/b2b-ingress-manager/internal/models"

	"github.com/google/nftables"
	"github.com/google/nftables/expr"
	"github.com/sirupsen/logrus"
)

// Manager handles nftables rules
type Manager struct {
	conn            *nftables.Conn
	logger          *logrus.Logger
	mu              sync.Mutex
	table           *nftables.Table
	chainPrerouting *nftables.Chain
	rng             *rand.Rand
}

// Config for the nftables manager
type Config struct {
	TableName string
	ChainName string
}

// NewManager creates a new nftables manager
func NewManager(config Config, logger *logrus.Logger) (*Manager, error) {
	conn := &nftables.Conn{}

	// Create a local random number generator
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	// Create the table if it doesn't exist
	table := &nftables.Table{
		Family: nftables.TableFamilyIPv4,
		Name:   config.TableName,
	}

	// Create the chain for prerouting if it doesn't exist
	chainPrerouting := &nftables.Chain{
		Name:     config.ChainName,
		Table:    table,
		Type:     nftables.ChainTypeNAT,
		Hooknum:  nftables.ChainHookPrerouting,
		Priority: nftables.ChainPriorityFilter,
	}

	manager := &Manager{
		conn:            conn,
		logger:          logger,
		table:           table,
		chainPrerouting: chainPrerouting,
		rng:             rng,
	}

	return manager, nil
}

// Initialize sets up the necessary tables and chains
func (m *Manager) Initialize() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Create the table
	m.table = m.conn.AddTable(m.table)

	// Create the chain
	m.chainPrerouting = m.conn.AddChain(m.chainPrerouting)

	// Apply the changes
	if err := m.conn.Flush(); err != nil {
		return fmt.Errorf("failed to initialize nftables: %v", err)
	}

	m.logger.Info("nftables initialized successfully")
	return nil
}

// ApplyRules applies the given rules to nftables
func (m *Manager) ApplyRules(rules []models.Rule, addresses map[uint][]models.Address) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Create a new connection to work with
	conn := &nftables.Conn{}

	// Get the table
	table := &nftables.Table{
		Family: nftables.TableFamilyIPv4,
		Name:   m.table.Name,
	}

	// Get the chain
	chainPrerouting := &nftables.Chain{
		Name:     m.chainPrerouting.Name,
		Table:    table,
		Type:     nftables.ChainTypeNAT,
		Hooknum:  nftables.ChainHookPrerouting,
		Priority: nftables.ChainPriorityFilter,
	}

	// Flush existing rules in the chain
	conn.FlushChain(chainPrerouting)

	// Add the new rules
	for _, rule := range rules {
		backendAddresses := addresses[rule.BackendSetID]
		if len(backendAddresses) == 0 {
			m.logger.Warnf("No available backend addresses for rule ID %d (BackendSet ID %d)", rule.ID, rule.BackendSetID)
			continue
		}

		// Generate expressions for this rule
		expressions, err := m.generateExpressionsForRule(rule, backendAddresses)
		if err != nil {
			m.logger.Errorf("Failed to generate expressions for rule ID %d: %v", rule.ID, err)
			continue
		}

		// Add the rule to the chain
		conn.AddRule(&nftables.Rule{
			Table:    table,
			Chain:    chainPrerouting,
			Exprs:    expressions,
			UserData: []byte(fmt.Sprintf("rule_id:%d", rule.ID)),
		})
	}

	// Apply the changes
	if err := conn.Flush(); err != nil {
		return fmt.Errorf("failed to apply nftables rules: %v", err)
	}

	m.logger.Info("nftables rules applied successfully")
	return nil
}

// generateExpressionsForRule creates nftables expressions for a rule
func (m *Manager) generateExpressionsForRule(rule models.Rule, addresses []models.Address) ([]expr.Any, error) {
	var expressions []expr.Any

	// Match protocol (TCP/UDP)
	var protoNum uint8
	switch rule.Protocol {
	case "tcp":
		protoNum = 6 // IPPROTO_TCP
	case "udp":
		protoNum = 17 // IPPROTO_UDP
	default:
		return nil, fmt.Errorf("unsupported protocol: %s", rule.Protocol)
	}

	// Add protocol match
	expressions = append(expressions,
		&expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1},
		&expr.Cmp{
			Op:       expr.CmpOpEq,
			Register: 1,
			Data:     []byte{protoNum},
		},
	)

	// Add destination port match
	expressions = append(expressions,
		&expr.Payload{
			DestRegister: 1,
			Base:         expr.PayloadBaseTransportHeader,
			Offset:       2, // Destination port offset in TCP/UDP header
			Len:          2,
		},
		&expr.Cmp{
			Op:       expr.CmpOpEq,
			Register: 1,
			Data:     byteOrder(uint16(rule.DestinationPort)),
		},
	)

	// Add source address match based on type
	switch rule.SourceDefinition.Type {
	case "ip":
		ip := net.ParseIP(rule.SourceDefinition.IPAddress)
		if ip == nil {
			return nil, fmt.Errorf("invalid IP address: %s", rule.SourceDefinition.IPAddress)
		}
		ip = ip.To4()
		if ip == nil {
			return nil, fmt.Errorf("not an IPv4 address: %s", rule.SourceDefinition.IPAddress)
		}

		expressions = append(expressions,
			&expr.Payload{
				DestRegister: 1,
				Base:         expr.PayloadBaseNetworkHeader,
				Offset:       12, // Source IP offset in IPv4 header
				Len:          4,
			},
			&expr.Cmp{
				Op:       expr.CmpOpEq,
				Register: 1,
				Data:     ip,
			},
		)

	case "subnet":
		_, ipnet, err := net.ParseCIDR(rule.SourceDefinition.Subnet)
		if err != nil {
			return nil, fmt.Errorf("invalid subnet: %s, error: %v", rule.SourceDefinition.Subnet, err)
		}

		ones, _ := ipnet.Mask.Size()
		mask := net.CIDRMask(ones, 32)

		expressions = append(expressions,
			&expr.Payload{
				DestRegister: 1,
				Base:         expr.PayloadBaseNetworkHeader,
				Offset:       12, // Source IP offset in IPv4 header
				Len:          4,
			},
			&expr.Bitwise{
				SourceRegister: 1,
				DestRegister:   1,
				Len:            4,
				Mask:           mask,
				Xor:            []byte{0, 0, 0, 0},
			},
			&expr.Cmp{
				Op:       expr.CmpOpEq,
				Register: 1,
				Data:     ipnet.IP.To4(),
			},
		)

	case "range":
		startIP := net.ParseIP(rule.SourceDefinition.RangeStart).To4()
		endIP := net.ParseIP(rule.SourceDefinition.RangeEnd).To4()

		if startIP == nil || endIP == nil {
			return nil, fmt.Errorf("invalid IP range: %s - %s", rule.SourceDefinition.RangeStart, rule.SourceDefinition.RangeEnd)
		}

		expressions = append(expressions,
			&expr.Payload{
				DestRegister: 1,
				Base:         expr.PayloadBaseNetworkHeader,
				Offset:       12, // Source IP offset in IPv4 header
				Len:          4,
			},
			&expr.Range{
				Op:       expr.CmpOpEq,
				Register: 1,
				FromData: startIP,
				ToData:   endIP,
			},
		)
	}

	// Select a random backend address
	selectedAddress := addresses[m.rng.Intn(len(addresses))]
	destIP := net.ParseIP(selectedAddress.IP).To4()
	if destIP == nil {
		return nil, fmt.Errorf("invalid backend IP address: %s", selectedAddress.IP)
	}

	// Add DNAT target
	expressions = append(expressions,
		&expr.Immediate{
			Register: 1,
			Data:     destIP,
		},
		&expr.Immediate{
			Register: 2,
			Data:     byteOrder(uint16(selectedAddress.Port)),
		},
		&expr.NAT{
			Type:        expr.NATTypeDestNAT,
			Family:      2, // AF_INET
			RegAddrMin:  1,
			RegProtoMin: 2,
		},
	)

	return expressions, nil
}

// byteOrder converts a uint16 to network byte order
func byteOrder(port uint16) []byte {
	bytes := make([]byte, 2)
	bytes[0] = byte(port >> 8)
	bytes[1] = byte(port)
	return bytes
}

// Cleanup removes nftables resources
func (m *Manager) Cleanup() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Delete the chain
	m.conn.DelChain(m.chainPrerouting)

	// Delete the table
	m.conn.DelTable(m.table)

	// Apply the changes
	if err := m.conn.Flush(); err != nil {
		return fmt.Errorf("failed to cleanup nftables: %v", err)
	}

	m.logger.Info("nftables resources cleaned up")
	return nil
}
