package driver

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// serverProvisionSpec captures the per-engine details needed to provision a
// database on a running server or in a Docker container. The three server
// drivers (postgres/mysql/mssql) fill this in and delegate to the shared
// provisionServer / provisionDocker helpers below.
type serverProvisionSpec struct {
	driverName string
	// maintenanceDB is the database to connect to in order to issue
	// CREATE DATABASE (e.g. "postgres", "master", or "" for MySQL).
	maintenanceDB string
	defaultPort   int
	// dockerImage and dockerEnv build the container; passwordEnvKey names the
	// container env var that carries the admin password.
	dockerImage   string
	passwordEnvFn func(pw string) map[string]string
	// createSQL builds the CREATE DATABASE statement for a (quoted) name.
	createSQL func(name string) string
}

// serverFields are the inputs collected for the "running server" mode.
func serverFields(defaultPort int) []ProvisionField {
	return []ProvisionField{
		{Key: "host", Label: "Host", Default: "localhost"},
		{Key: "port", Label: "Port", Default: strconv.Itoa(defaultPort)},
		{Key: "user", Label: "Admin user", Default: "", Placeholder: "postgres / root / sa"},
		{Key: "password", Label: "Admin password", Password: true},
		{Key: "db_name", Label: "New database name", Placeholder: "myapp"},
	}
}

// dockerFields are the inputs collected for the "docker container" mode.
func dockerFields(defaultPort int) []ProvisionField {
	return []ProvisionField{
		{Key: "container", Label: "Container name", Default: ""},
		{Key: "password", Label: "Admin password", Password: true},
		{Key: "port", Label: "Host port", Default: strconv.Itoa(defaultPort)},
		{Key: "db_name", Label: "New database name", Placeholder: "myapp"},
	}
}

// provisionServer connects to a running server's maintenance database and runs
// CREATE DATABASE, returning a connection to the new database.
func (s serverProvisionSpec) provisionServer(ctx context.Context, values map[string]string) (ProvisionResult, error) {
	dbName := strings.TrimSpace(values["db_name"])
	if dbName == "" {
		return ProvisionResult{}, fmt.Errorf("%s: a new database name is required", s.driverName)
	}
	port := s.defaultPort
	if p := strings.TrimSpace(values["port"]); p != "" {
		if n, err := strconv.Atoi(p); err == nil && n > 0 {
			port = n
		}
	}
	host := strings.TrimSpace(values["host"])
	if host == "" {
		host = "localhost"
	}

	admin := ConnInfo{
		Driver:   s.driverName,
		Host:     host,
		Port:     port,
		User:     strings.TrimSpace(values["user"]),
		Password: values["password"],
		Database: s.maintenanceDB,
	}

	if err := s.createDatabase(ctx, admin, dbName); err != nil {
		return ProvisionResult{}, err
	}

	// Connection to the newly-created database (password not persisted).
	newInfo := admin
	newInfo.Database = dbName
	newInfo.Password = ""

	return ProvisionResult{
		Info:  newInfo,
		Steps: []string{fmt.Sprintf("Created database %q on %s:%d", dbName, host, port)},
	}, nil
}

// provisionDocker starts a fresh container, waits for it to accept connections,
// then creates the database inside it.
func (s serverProvisionSpec) provisionDocker(ctx context.Context, values map[string]string) (ProvisionResult, error) {
	if !dockerAvailable() {
		return ProvisionResult{}, fmt.Errorf("docker is not installed or the daemon is not running")
	}
	dbName := strings.TrimSpace(values["db_name"])
	if dbName == "" {
		return ProvisionResult{}, fmt.Errorf("%s: a new database name is required", s.driverName)
	}
	name := strings.TrimSpace(values["container"])
	if name == "" {
		name = "sqwee-" + s.driverName + "-" + strconv.FormatInt(time.Now().Unix(), 10)
	}
	password := values["password"]
	if password == "" {
		// Generate a password that satisfies SQL Server's complexity rules
		// (upper, lower, digit, symbol, 8+ chars) so it works for all engines.
		password = "Sqwee_" + strconv.FormatInt(time.Now().UnixNano(), 36) + "9!"
	}
	hostPort := freeHostPort(values, s.defaultPort)

	spec := dockerSpec{
		Image:         s.dockerImage,
		Env:           s.passwordEnvFn(password),
		ContainerPort: s.defaultPort,
		HostPort:      hostPort,
		Name:          name,
	}

	var steps []string
	container, err := runDockerContainer(ctx, spec)
	if err != nil {
		return ProvisionResult{}, err
	}
	steps = append(steps, "Started Docker container "+container+" ("+s.dockerImage+")")

	// The admin user inside the container.
	admin := ConnInfo{
		Driver:   s.driverName,
		Host:     "localhost",
		Port:     hostPort,
		User:     s.dockerAdminUser(),
		Password: password,
		Database: s.maintenanceDB,
	}

	d := ByName(s.driverName)
	waitCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	if err := waitForServer(waitCtx, d, admin); err != nil {
		cancel()
		removeDockerContainer(container)
		return ProvisionResult{}, fmt.Errorf("container started but DB never became ready (removed): %w", err)
	}
	cancel()
	steps = append(steps, "Server is accepting connections")

	if err := s.createDatabase(ctx, admin, dbName); err != nil {
		return ProvisionResult{}, fmt.Errorf("container ready but CREATE DATABASE failed: %w", err)
	}
	steps = append(steps, fmt.Sprintf("Created database %q", dbName))

	newInfo := admin
	newInfo.Database = dbName
	newInfo.Password = ""

	return ProvisionResult{
		Info:         newInfo,
		Steps:        steps,
		Container:    container,
		PasswordHint: password,
	}, nil
}

// createDatabase connects to the maintenance DB and issues CREATE DATABASE.
func (s serverProvisionSpec) createDatabase(ctx context.Context, admin ConnInfo, dbName string) error {
	d := ByName(s.driverName)
	if d == nil {
		return fmt.Errorf("driver %q not registered", s.driverName)
	}
	connCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	conn, err := d.Connect(connCtx, admin)
	if err != nil {
		return fmt.Errorf("%s: cannot reach server: %w", s.driverName, err)
	}
	defer conn.Close()

	if _, err := conn.Exec(connCtx, s.createSQL(dbName)); err != nil {
		return fmt.Errorf("%s: CREATE DATABASE failed: %w", s.driverName, err)
	}
	return nil
}

// dockerAdminUser returns the default superuser the official image creates.
func (s serverProvisionSpec) dockerAdminUser() string {
	switch s.driverName {
	case "postgres":
		return "postgres"
	case "mysql":
		return "root"
	case "mssql":
		return "sa"
	default:
		return ""
	}
}
