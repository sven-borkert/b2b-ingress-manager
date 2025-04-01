# B2B Ingress Manager

A Golang application to manage nftables configuration on a Linux server, allowing dynamic routing of incoming connections to different backends based on source IP, subnet, or address range.

## Features

- Manage nftables firewall rules from a PostgreSQL database
- Route incoming connections to different backend servers based on:
  - Source IP address
  - Source subnet
  - Source IP range
- Load balancing across multiple backend servers
- Continuous health checking of backend servers
- Web API for configuration management
- Change logging and availability history
- Non-disruptive configuration updates

## Requirements

- Linux with nftables support
- PostgreSQL database
- Go 1.21 or higher

## Installation

1. Clone the repository:

```bash
git clone https://github.com/sven-borkert/b2b-ingress-manager.git
cd nftables-manager
```

2. Build the application:

```bash
go build -o nftables-manager ./cmd/server
```

3. Create the PostgreSQL database:

```sql
CREATE DATABASE nftables;
```

## Database Schema

The application uses the following database schema:

- `backends`: Destination servers
- `addresses`: Backend server addresses
- `backend_sets`: Groups of backends for load balancing
- `source_definitions`: Source IP, subnet, or range configurations
- `rules`: Routing rules connecting sources to backend sets
- `config_changes`: Log of configuration changes
- `availability_logs`: Log of backend availability changes

The database tables will be automatically created when the application first starts.

## Configuration

The application can be configured in two ways:

### Configuration File

You can configure the application using a YAML configuration file:

```yaml
# NFTables Manager Configuration

# Log level: debug, info, warn, error
log_level: info

# Database connection parameters
db_host: localhost
db_port: 5432
db_user: postgres
db_password: ""
db_name: nftables
db_sslmode: disable

# API server configuration
api_listen: ":8080"

# Update and health check intervals
update_interval: 30s
health_timeout: 5s
health_interval: 60s

# NFTables configuration
nft_table: nat
nft_chain: prerouting
```

By default, the application will look for a `config.yaml` file in the current directory. You can specify a different configuration file using the `-config` flag.

### Command-line Flags

The application can also be configured via command-line flags:

```
Usage of nftables-manager:
  -config string
        Path to configuration file (default "config.yaml")
  -api-listen string
        API server listen address (default ":8080")
  -db-host string
        PostgreSQL host (default "localhost")
  -db-name string
        PostgreSQL database name (default "nftables")
  -db-password string
        PostgreSQL password
  -db-port int
        PostgreSQL port (default 5432)
  -db-sslmode string
        PostgreSQL SSL mode (default "disable")
  -db-user string
        PostgreSQL user (default "postgres")
  -health-interval duration
        Health check interval (default 1m0s)
  -health-timeout duration
        Health check timeout (default 5s)
  -log-level string
        Log level (debug, info, warn, error) (default "info")
  -nft-chain string
        NFTables chain name (default "prerouting")
  -nft-table string
        NFTables table name (default "nat")
  -update-interval duration
        NFTables update interval (default 30s)
```

Command-line flags take precedence over configuration file settings. This allows you to override specific configuration options when needed.

## Running the Application

Start the application with a configuration file:

```bash
sudo ./nftables-manager -config=/path/to/config.yaml
```

Or with command-line parameters:

```bash
sudo ./nftables-manager \
  -db-host localhost \
  -db-port 5432 \
  -db-user postgres \
  -db-password your_password \
  -db-name nftables \
  -api-listen :8080
```

Note: `sudo` privileges are required to modify nftables rules.

## API Endpoints

### Backends

- `GET /api/backends` - List all backends
- `GET /api/backends/:id` - Get a specific backend
- `POST /api/backends` - Create a new backend
- `PUT /api/backends/:id` - Update a backend
- `DELETE /api/backends/:id` - Delete a backend

### Backend Addresses

- `POST /api/backends/:id/addresses` - Add an address to a backend
- `PUT /api/addresses/:id` - Update an address
- `DELETE /api/addresses/:id` - Delete an address

### Backend Sets

- `GET /api/backend-sets` - List all backend sets
- `GET /api/backend-sets/:id` - Get a specific backend set
- `POST /api/backend-sets` - Create a new backend set
- `PUT /api/backend-sets/:id` - Update a backend set
- `DELETE /api/backend-sets/:id` - Delete a backend set

### Source Definitions

- `GET /api/source-definitions` - List all source definitions
- `GET /api/source-definitions/:id` - Get a specific source definition
- `POST /api/source-definitions` - Create a new source definition
- `PUT /api/source-definitions/:id` - Update a source definition
- `DELETE /api/source-definitions/:id` - Delete a source definition

### Rules

- `GET /api/rules` - List all rules
- `GET /api/rules/:id` - Get a specific rule
- `POST /api/rules` - Create a new rule
- `PUT /api/rules/:id` - Update a rule
- `DELETE /api/rules/:id` - Delete a rule

### Logs

- `GET /api/logs/config` - Get configuration change logs
- `GET /api/logs/availability` - Get backend availability logs

## Example API Usage

### Creating a Backend

```bash
curl -X POST http://localhost:8080/api/backends \
  -H "Content-Type: application/json" \
  -d '{
    "name": "web-servers",
    "description": "Web server pool"
  }'
```

### Adding an Address to a Backend

```bash
curl -X POST http://localhost:8080/api/backends/1/addresses \
  -H "Content-Type: application/json" \
  -d '{
    "ip": "192.168.1.10",
    "port": 80
  }'
```

### Creating a Backend Set

```bash
curl -X POST http://localhost:8080/api/backend-sets \
  -H "Content-Type: application/json" \
  -d '{
    "name": "internal-web",
    "description": "Internal web servers",
    "backends": [
      {
        "id": 1
      },
      {
        "id": 2
      }
    ]
  }'
```

Note: The `backends` field must be an array of backend objects, where each object contains the `id` of an existing backend. You can get the list of available backends using the `GET /api/backends` endpoint.

### Creating a Source Definition

```bash
curl -X POST http://localhost:8080/api/source-definitions \
  -H "Content-Type: application/json" \
  -d '{
    "name": "internal-network",
    "description": "Internal company network",
    "type": "subnet",
    "subnet": "10.0.0.0/8"
  }'
```

### Creating a Rule

```bash
curl -X POST http://localhost:8080/api/rules \
  -H "Content-Type: application/json" \
  -d '{
    "source_definition_id": 1,
    "destination_port": 80,
    "protocol": "tcp",
    "backend_set_id": 1,
    "priority": 100,
    "enabled": true
  }'
```
