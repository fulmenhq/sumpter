package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/fulmenhq/sumpter/internal/config"
	"github.com/fulmenhq/sumpter/internal/logging"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

// Helper function to write formatted output with error handling
func writeFprintf(w io.Writer, format string, args ...interface{}) error {
	_, err := fmt.Fprintf(w, format, args...)
	return err
}

// Helper function to write line output with error handling
func writeFprintln(w io.Writer, args ...interface{}) error {
	_, err := fmt.Fprintln(w, args...)
	return err
}

type EnvData struct {
	System      SystemInfo        `json:"system"`
	Network     NetworkInfo       `json:"network,omitempty"`
	Variables   map[string]string `json:"variables"`
	Stats       EnvStats          `json:"stats"`
	XML         XMLCapabilities   `json:"xml,omitempty"`
	Application ApplicationPaths  `json:"application,omitempty"`
}

type SystemInfo struct {
	OS           string    `json:"os"`
	Architecture string    `json:"architecture"`
	GoVersion    string    `json:"goVersion"`
	NumCPU       int       `json:"numCPU"`
	Hostname     string    `json:"hostname"`
	WorkingDir   string    `json:"workingDir"`
	Timestamp    time.Time `json:"timestamp"`
	ExternalIP   string    `json:"externalIP,omitempty"`
}

type NetworkInfo struct {
	Interfaces []NetworkInterface `json:"interfaces,omitempty"`
	ExternalIP string             `json:"externalIP,omitempty"`
}

type NetworkInterface struct {
	Name    string `json:"name"`
	Address string `json:"address"`
	Status  string `json:"status"`
}

type EnvStats struct {
	TotalVars    int `json:"totalVars"`
	FilteredVars int `json:"filteredVars"`
	KeyVarsCount int `json:"keyVarsCount"`
}

type XMLCapabilities struct {
	StreamingSupported bool     `json:"streamingSupported"`
	Encodings          []string `json:"encodings"`
	MaxMemoryTarget    string   `json:"maxMemoryTarget"`
	SupportedOutputs   []string `json:"supportedOutputs"`
}

const xmlInputMemoryTarget = "<50MB RSS"

var (
	xmlSupportedEncodings = []string{"UTF-8", "UTF-16", "ISO-8859-1", "Windows-1252"}
	xmlSupportedOutputs   = []string{"JSON", "NDJSON", "Parquet"}
)

type ApplicationPaths struct {
	Home    string `json:"home"`
	WorkDir string `json:"workDir"`
	Cache   string `json:"cache"`
	Logs    string `json:"logs"`
	Configs string `json:"configs"`
	Temp    string `json:"temp"`
}

func NewEnvInfoCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "envinfo",
		Short: "Display environment and system information for Sumpter",
		Long: `Comprehensive environment inspection tool for Sumpter XML processing.

This command provides detailed insights into the system environment,
network configuration, and XML processing capabilities. It's designed
to help diagnose setup issues and validate system readiness for XML
transformation workflows.

Features:
- System information (OS, architecture, Go version, CPU cores)
- Environment variable inspection with security filtering
- Network interface detection (optional)
- External IP detection (optional)
- XML processing capabilities assessment
- Multiple output formats (human-readable, JSON, export)

Security: Sensitive environment variables are automatically redacted.

Subcommands:
  system    Show system information only
  paths     Show application paths only
  vars      Show environment variables only
  xml       Show XML processing capabilities only
  network   Show network information only`,
		RunE: runEnvInfoMain,
	}

	// Add subcommands
	cmd.AddCommand(newEnvInfoSystemCommand())
	cmd.AddCommand(newEnvInfoPathsCommand())
	cmd.AddCommand(newEnvInfoVarsCommand())
	cmd.AddCommand(newEnvInfoXMLCommand())
	cmd.AddCommand(newEnvInfoNetworkCommand())

	// Main command flags (for backward compatibility)
	cmd.Flags().BoolP("all", "a", false, "Show all environment variables")
	cmd.Flags().BoolP("export", "e", false, "Output in shell export format")
	cmd.Flags().String("filter", "", "Filter variables by key (case-insensitive substring match)")
	cmd.Flags().BoolP("json", "j", false, "Output in JSON format")
	cmd.Flags().Bool("network", false, "Include network interface information")
	cmd.Flags().Bool("external-ip", false, "Include external IP detection")
	cmd.Flags().BoolP("verbose", "v", false, "Enable verbose output")
	cmd.Flags().Bool("xml", false, "Include XML processing capabilities information")

	return cmd
}

// runEnvInfoMain handles the main envinfo command (backward compatibility)
func runEnvInfoMain(cmd *cobra.Command, args []string) error {
	log := logging.Component("envinfo")

	// Get flag values
	all, _ := cmd.Flags().GetBool("all")
	exportFormat, _ := cmd.Flags().GetBool("export")
	filter, _ := cmd.Flags().GetString("filter")
	jsonFormat, _ := cmd.Flags().GetBool("json")
	network, _ := cmd.Flags().GetBool("network")
	externalIP, _ := cmd.Flags().GetBool("external-ip")
	verbose, _ := cmd.Flags().GetBool("verbose")
	xmlInfo, _ := cmd.Flags().GetBool("xml")

	if verbose {
		log.Info("Starting main envinfo command")
	}

	data, err := collectEnvironmentData(all, filter, network, externalIP, xmlInfo)
	if err != nil {
		return fmt.Errorf("failed to collect environment data: %w", err)
	}

	if jsonFormat {
		// Apply PII redaction to environment variables before JSON output
		if data.Variables != nil {
			redactedVars := make(map[string]string)
			for key, value := range data.Variables {
				redactedVars[key] = maybeRedact(key, value)
			}
			data.Variables = redactedVars
		}

		jsonData, err := json.MarshalIndent(data, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to format JSON output: %w", err)
		}
		if _, err := fmt.Fprintln(cmd.OutOrStdout(), string(jsonData)); err != nil {
			return fmt.Errorf("failed to write JSON output: %w", err)
		}
		return nil
	}

	return outputHumanReadable(cmd, data, exportFormat)
}

func collectEnvironmentData(all bool, filter string, includeNetwork, includeExternalIP, includeXML bool) (*EnvData, error) {
	// Create component logger for envinfo command
	log := logging.Component("envinfo")

	log.Info("Starting environment data collection",
		zap.Bool("all_vars", all),
		zap.String("filter", filter),
		zap.Bool("include_network", includeNetwork),
		zap.Bool("include_external_ip", includeExternalIP),
		zap.Bool("include_xml", includeXML))

	variables := make(map[string]string)
	totalVars := 0
	keyVarsCount := 0

	keyVariables := []string{"HOME", "USER", "PATH", "SHELL", "TERM", "LANG", "PWD", "SUMPTER_HOME", "XML_CATALOG_FILES"}
	envVars := os.Environ()
	totalVars = len(envVars)

	// Collect environment variables
	if all {
		for _, env := range envVars {
			pair := strings.SplitN(env, "=", 2)
			key := pair[0]
			value := ""
			if len(pair) > 1 {
				value = pair[1]
			}
			if filter == "" || strings.Contains(strings.ToLower(key), strings.ToLower(filter)) {
				variables[key] = value
			}
		}
	} else {
		for _, key := range keyVariables {
			value := os.Getenv(key)
			if value != "" {
				if filter == "" || strings.Contains(strings.ToLower(key), strings.ToLower(filter)) {
					variables[key] = value
					keyVarsCount++
				}
			}
		}
		if filter != "" {
			for _, env := range envVars {
				pair := strings.SplitN(env, "=", 2)
				key := pair[0]
				value := ""
				if len(pair) > 1 {
					value = pair[1]
				}
				if !contains(keyVariables, key) && strings.Contains(strings.ToLower(key), strings.ToLower(filter)) {
					variables[key] = value
				}
			}
		}
	}

	// System information
	hostname, _ := os.Hostname()
	wd, _ := os.Getwd()

	systemInfo := SystemInfo{
		OS:           runtime.GOOS,
		Architecture: runtime.GOARCH,
		GoVersion:    runtime.Version(),
		NumCPU:       runtime.NumCPU(),
		Hostname:     hostname,
		WorkingDir:   wd,
		Timestamp:    time.Now(),
	}

	// External IP
	if includeExternalIP {
		if ip, err := getExternalIP(); err == nil {
			systemInfo.ExternalIP = ip
		}
	}

	// Network interfaces
	var networkInfo *NetworkInfo
	if includeNetwork {
		if interfaces, err := getNetworkInterfaces(); err == nil {
			networkInfo = &NetworkInfo{Interfaces: interfaces}
		}
	}

	// XML capabilities
	var xmlCapabilities *XMLCapabilities
	if includeXML {
		caps := collectXMLCapabilities()
		xmlCapabilities = &caps
	}

	// Application paths
	log.Debug("Resolving application paths")
	paths, err := config.ResolvePaths("", "")
	if err != nil {
		log.Warn("Failed to resolve application paths", zap.Error(err))
		paths = &config.Paths{} // Use empty paths if resolution fails
	} else {
		log.Debug("Application paths resolved successfully",
			zap.String("home", paths.Home),
			zap.String("workdir", paths.WorkDir))
	}

	applicationPaths := ApplicationPaths{
		Home:    paths.Home,
		WorkDir: paths.WorkDir,
		Cache:   paths.Cache,
		Logs:    paths.Logs,
		Configs: paths.Configs,
		Temp:    paths.Temp,
	}

	stats := EnvStats{
		TotalVars:    totalVars,
		FilteredVars: len(variables),
		KeyVarsCount: keyVarsCount,
	}

	data := &EnvData{
		System:      systemInfo,
		Variables:   variables,
		Stats:       stats,
		Application: applicationPaths,
	}

	if networkInfo != nil {
		data.Network = *networkInfo
	}

	if xmlCapabilities != nil {
		data.XML = *xmlCapabilities
	}

	log.Info("Environment data collection completed",
		zap.Int("total_vars", totalVars),
		zap.Int("filtered_vars", len(variables)),
		zap.Bool("has_network", networkInfo != nil),
		zap.Bool("has_xml", xmlCapabilities != nil))

	return data, nil
}

func getExternalIP() (string, error) {
	services := []string{
		"https://api.ipify.org",
		"https://icanhazip.com",
		"https://ifconfig.me",
	}

	client := &http.Client{Timeout: 5 * time.Second}

	for _, service := range services {
		if ip, err := fetchIPFromService(client, service); err == nil {
			return strings.TrimSpace(ip), nil
		}
	}
	return "", fmt.Errorf("failed to detect external IP from all services")
}

func fetchIPFromService(client *http.Client, url string) (string, error) {
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			// Log the error but don't fail the function
			// In a real application, you might want to use a logger here
			_ = closeErr // explicitly ignore the error
		}
	}()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("service returned status %d", resp.StatusCode)
	}

	body := make([]byte, 1024)
	n, err := resp.Body.Read(body)
	if err != nil && err.Error() != "EOF" {
		return "", err
	}

	return string(body[:n]), nil
}

func getNetworkInterfaces() ([]NetworkInterface, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	var result []NetworkInterface
	for _, iface := range interfaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			result = append(result, NetworkInterface{
				Name:    iface.Name,
				Address: addr.String(),
				Status:  "up",
			})
		}
	}
	return result, nil
}

func outputHumanReadable(cmd *cobra.Command, data *EnvData, exportFormat bool) error {
	out := cmd.OutOrStdout()

	// System Information Section
	if _, err := fmt.Fprintln(out, "🖥️  System Information"); err != nil {
		return fmt.Errorf("failed to write system info header: %w", err)
	}
	if _, err := fmt.Fprintln(out, "=================================================="); err != nil {
		return fmt.Errorf("failed to write system info separator: %w", err)
	}
	if _, err := fmt.Fprintf(out, "%-16s | %s\n", "OS", data.System.OS); err != nil {
		return fmt.Errorf("failed to write OS info: %w", err)
	}
	if _, err := fmt.Fprintf(out, "%-16s | %s\n", "Architecture", data.System.Architecture); err != nil {
		return fmt.Errorf("failed to write architecture info: %w", err)
	}
	if err := writeFprintf(out, "%-16s | %s\n", "Go Version", data.System.GoVersion); err != nil {
		return fmt.Errorf("failed to write Go version: %w", err)
	}
	if err := writeFprintf(out, "%-16s | %d\n", "CPU Cores", data.System.NumCPU); err != nil {
		return fmt.Errorf("failed to write CPU cores: %w", err)
	}
	if err := writeFprintf(out, "%-16s | %s\n", "Hostname", data.System.Hostname); err != nil {
		return fmt.Errorf("failed to write hostname: %w", err)
	}
	if err := writeFprintf(out, "%-16s | %s\n", "Working Dir", data.System.WorkingDir); err != nil {
		return fmt.Errorf("failed to write working dir: %w", err)
	}
	if err := writeFprintf(out, "%-16s | %s\n", "Timestamp", data.System.Timestamp.Format(time.RFC3339)); err != nil {
		return fmt.Errorf("failed to write timestamp: %w", err)
	}
	if data.System.ExternalIP != "" {
		if err := writeFprintf(out, "%-16s | %s\n", "External IP", data.System.ExternalIP); err != nil {
			return fmt.Errorf("failed to write external IP: %w", err)
		}
	}
	if err := writeFprintln(out, ""); err != nil {
		return fmt.Errorf("failed to write newline: %w", err)
	}

	// XML Capabilities Section
	if data.XML.StreamingSupported {
		if err := writeFprintln(out, "📄 XML Processing Capabilities"); err != nil {
			return fmt.Errorf("failed to write XML capabilities header: %w", err)
		}
		if err := writeFprintln(out, "=================================================="); err != nil {
			return fmt.Errorf("failed to write XML capabilities separator: %w", err)
		}
		if err := writeFprintf(out, "%-16s | %t\n", "Streaming", data.XML.StreamingSupported); err != nil {
			return fmt.Errorf("failed to write streaming info: %w", err)
		}
		if err := writeFprintf(out, "%-16s | %s\n", "Memory Target", data.XML.MaxMemoryTarget); err != nil {
			return fmt.Errorf("failed to write memory target: %w", err)
		}
		if err := writeFprintf(out, "%-16s | %s\n", "Encodings", strings.Join(data.XML.Encodings, ", ")); err != nil {
			return fmt.Errorf("failed to write encodings: %w", err)
		}
		if err := writeFprintf(out, "%-16s | %s\n", "Outputs", strings.Join(data.XML.SupportedOutputs, ", ")); err != nil {
			return fmt.Errorf("failed to write outputs: %w", err)
		}
		if err := writeFprintln(out, ""); err != nil {
			return fmt.Errorf("failed to write XML capabilities newline: %w", err)
		}
	}

	// Application Environment Section
	if err := writeFprintln(out, "🏠 Application Environment"); err != nil {
		return fmt.Errorf("failed to write app environment header: %w", err)
	}
	if err := writeFprintln(out, "=================================================="); err != nil {
		return fmt.Errorf("failed to write app environment separator: %w", err)
	}
	if err := writeFprintf(out, "%-16s | %s\n", "Home", data.Application.Home); err != nil {
		return fmt.Errorf("failed to write home path: %w", err)
	}
	if err := writeFprintf(out, "%-16s | %s\n", "WorkDir", data.Application.WorkDir); err != nil {
		return fmt.Errorf("failed to write workdir path: %w", err)
	}
	if err := writeFprintf(out, "%-16s | %s\n", "Cache", data.Application.Cache); err != nil {
		return fmt.Errorf("failed to write cache path: %w", err)
	}
	if err := writeFprintf(out, "%-16s | %s\n", "Logs", data.Application.Logs); err != nil {
		return fmt.Errorf("failed to write logs path: %w", err)
	}
	if err := writeFprintf(out, "%-16s | %s\n", "Configs", data.Application.Configs); err != nil {
		return fmt.Errorf("failed to write configs path: %w", err)
	}
	if err := writeFprintf(out, "%-16s | %s\n", "Temp", data.Application.Temp); err != nil {
		return fmt.Errorf("failed to write temp path: %w", err)
	}
	if err := writeFprintln(out, ""); err != nil {
		return fmt.Errorf("failed to write app environment newline: %w", err)
	}

	// Network Interfaces Section
	if len(data.Network.Interfaces) > 0 {
		if err := writeFprintln(out, "🌐 Network Interfaces"); err != nil {
			return fmt.Errorf("failed to write network interfaces header: %w", err)
		}
		if err := writeFprintln(out, "=================================================="); err != nil {
			return fmt.Errorf("failed to write network interfaces separator: %w", err)
		}
		for _, iface := range data.Network.Interfaces {
			if err := writeFprintf(out, "%-20s | %s (%s)\n", iface.Name, iface.Address, iface.Status); err != nil {
				return fmt.Errorf("failed to write network interface %s: %w", iface.Name, err)
			}
		}
		if err := writeFprintln(out, ""); err != nil {
			return fmt.Errorf("failed to write network interfaces newline: %w", err)
		}
	}

	// Environment Variables Section
	if err := writeFprintln(out, "🌍 Environment Variables"); err != nil {
		return fmt.Errorf("failed to write environment variables header: %w", err)
	}
	if err := writeFprintln(out, "=================================================="); err != nil {
		return fmt.Errorf("failed to write environment variables separator: %w", err)
	}

	if len(data.Variables) == 0 {
		if err := writeFprintln(out, "No environment variables found matching the filter."); err != nil {
			return fmt.Errorf("failed to write no variables message: %w", err)
		}
	} else {
		if exportFormat {
			for key, value := range data.Variables {
				// Redact sensitive values
				value = maybeRedact(key, value)
				if err := writeFprintf(out, "export %s=%q\n", key, value); err != nil {
					return fmt.Errorf("failed to write export for %s: %w", key, err)
				}
			}
		} else {
			maxKeyLength := 20
			for key := range data.Variables {
				if len(key) > maxKeyLength {
					maxKeyLength = len(key)
				}
			}

			for key, value := range data.Variables {
				// Redact sensitive values
				value = maybeRedact(key, value)

				if len(value) > 50 {
					value = value[:50] + "..."
				}
				if err := writeFprintf(out, "%-*s | %s\n", maxKeyLength, key, value); err != nil {
					return fmt.Errorf("failed to write variable %s: %w", key, err)
				}
			}
		}
	}

	// Stats
	if !exportFormat {
		if err := writeFprintln(out, ""); err != nil {
			return fmt.Errorf("failed to write stats newline: %w", err)
		}
		if err := writeFprintln(out, "📊 Stats"); err != nil {
			return fmt.Errorf("failed to write stats header: %w", err)
		}
		if err := writeFprintln(out, "=================================================="); err != nil {
			return fmt.Errorf("failed to write stats separator: %w", err)
		}
		if err := writeFprintf(out, "%-16s | %d\n", "Total Vars", data.Stats.TotalVars); err != nil {
			return fmt.Errorf("failed to write total vars: %w", err)
		}
		if err := writeFprintf(out, "%-16s | %d\n", "Filtered Vars", data.Stats.FilteredVars); err != nil {
			return fmt.Errorf("failed to write filtered vars: %w", err)
		}
		if err := writeFprintf(out, "%-16s | %d\n", "Key Vars", data.Stats.KeyVarsCount); err != nil {
			return fmt.Errorf("failed to write key vars: %w", err)
		}
	}

	return nil
}

func contains(slice []string, str string) bool {
	for _, s := range slice {
		if s == str {
			return true
		}
	}
	return false
}

// maybeRedact redacts sensitive environment variable values
func maybeRedact(key, value string) string {
	// Redact common secret patterns (case-insensitive)
	secretKeys := []string{
		"secret", "token", "apikey", "api_key", "password", "passwd", "pwd",
		"credential", "auth", "bearer", "jwt", "session", "key", "cert",
		"xml_catalog", "xml_schema", "database_url", "db_url",
	}

	keyLower := strings.ToLower(key)
	for _, pattern := range secretKeys {
		if strings.Contains(keyLower, pattern) {
			return "***redacted***"
		}
	}
	return value
}

// Subcommand implementations

func newEnvInfoSystemCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "system",
		Short: "Show system information only",
		Long:  "Display system information including OS, architecture, Go version, CPU cores, hostname, and working directory.",
		RunE: func(cmd *cobra.Command, args []string) error {
			log := logging.Component("envinfo.system")

			jsonFormat, _ := cmd.Flags().GetBool("json")

			log.Info("Collecting system information")

			systemInfo := collectSystemInfo()

			if jsonFormat {
				jsonData, err := json.MarshalIndent(systemInfo, "", "  ")
				if err != nil {
					return fmt.Errorf("failed to format JSON output: %w", err)
				}
				if _, err := fmt.Fprintln(cmd.OutOrStdout(), string(jsonData)); err != nil {
					return fmt.Errorf("failed to write JSON output: %w", err)
				}
				return nil
			}

			return outputSystemInfo(cmd, systemInfo)
		},
	}

	cmd.Flags().BoolP("json", "j", false, "Output in JSON format")

	return cmd
}

func newEnvInfoPathsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "paths",
		Short: "Show application paths only",
		Long:  "Display Sumpter application directory paths including home, workdir, cache, logs, configs, and temp directories.",
		RunE: func(cmd *cobra.Command, args []string) error {
			log := logging.Component("envinfo.paths")

			jsonFormat, _ := cmd.Flags().GetBool("json")

			log.Info("Collecting application paths")

			paths, err := config.ResolvePaths("", "")
			if err != nil {
				return fmt.Errorf("failed to resolve paths: %w", err)
			}

			applicationPaths := ApplicationPaths{
				Home:    paths.Home,
				WorkDir: paths.WorkDir,
				Cache:   paths.Cache,
				Logs:    paths.Logs,
				Configs: paths.Configs,
				Temp:    paths.Temp,
			}

			if jsonFormat {
				jsonData, err := json.MarshalIndent(applicationPaths, "", "  ")
				if err != nil {
					return fmt.Errorf("failed to format JSON output: %w", err)
				}
				if _, err := fmt.Fprintln(cmd.OutOrStdout(), string(jsonData)); err != nil {
					return fmt.Errorf("failed to write JSON output: %w", err)
				}
				return nil
			}

			return outputApplicationPaths(cmd, applicationPaths)
		},
	}

	cmd.Flags().BoolP("json", "j", false, "Output in JSON format")

	return cmd
}

func newEnvInfoVarsCommand() *cobra.Command {
	var (
		all        bool
		filter     string
		jsonFormat bool
	)

	cmd := &cobra.Command{
		Use:   "vars",
		Short: "Show environment variables only",
		Long: `Display environment variables with security filtering.

This subcommand shows environment variables, with special focus on SUMPTER_
prefixed variables. Sensitive values are automatically redacted for security.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			log := logging.Component("envinfo.vars")

			log.Info("Collecting environment variables", zap.Bool("all", all), zap.String("filter", filter))

			variables := collectEnvironmentVariables(all, filter)

			if jsonFormat {
				// Apply PII redaction to values before JSON output
				redactedVars := make(map[string]string)
				for key, value := range variables {
					redactedVars[key] = maybeRedact(key, value)
				}

				jsonData, err := json.MarshalIndent(redactedVars, "", "  ")
				if err != nil {
					return fmt.Errorf("failed to format JSON output: %w", err)
				}
				if _, err := fmt.Fprintln(cmd.OutOrStdout(), string(jsonData)); err != nil {
					return fmt.Errorf("failed to write JSON output: %w", err)
				}
				return nil
			}

			return outputEnvironmentVariables(cmd, variables)
		},
	}

	cmd.Flags().BoolVarP(&all, "all", "a", false, "Show all environment variables")
	cmd.Flags().StringVar(&filter, "filter", "", "Filter variables by key (case-insensitive substring match)")
	cmd.Flags().BoolVarP(&jsonFormat, "json", "j", false, "Output in JSON format")

	return cmd
}

func newEnvInfoXMLCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "xml",
		Short: "Show XML processing capabilities only",
		Long:  "Display XML processing capabilities including streaming support, encodings, and memory targets.",
		RunE: func(cmd *cobra.Command, args []string) error {
			log := logging.Component("envinfo.xml")

			jsonFormat, _ := cmd.Flags().GetBool("json")

			log.Info("Collecting XML capabilities")

			xmlCapabilities := collectXMLCapabilities()

			if jsonFormat {
				jsonData, err := json.MarshalIndent(xmlCapabilities, "", "  ")
				if err != nil {
					return fmt.Errorf("failed to format JSON output: %w", err)
				}
				if _, err := fmt.Fprintln(cmd.OutOrStdout(), string(jsonData)); err != nil {
					return fmt.Errorf("failed to write JSON output: %w", err)
				}
				return nil
			}

			return outputXMLCapabilities(cmd, xmlCapabilities)
		},
	}

	cmd.Flags().BoolP("json", "j", false, "Output in JSON format")

	return cmd
}

func newEnvInfoNetworkCommand() *cobra.Command {
	var (
		externalIP bool
		jsonFormat bool
	)

	cmd := &cobra.Command{
		Use:   "network",
		Short: "Show network information only",
		Long:  "Display network interface information and optionally external IP address.",
		RunE: func(cmd *cobra.Command, args []string) error {
			log := logging.Component("envinfo.network")

			log.Info("Collecting network information", zap.Bool("external_ip", externalIP))

			networkInfo, err := collectNetworkInfo(externalIP)
			if err != nil {
				return fmt.Errorf("failed to collect network info: %w", err)
			}

			if jsonFormat {
				jsonData, err := json.MarshalIndent(networkInfo, "", "  ")
				if err != nil {
					return fmt.Errorf("failed to format JSON output: %w", err)
				}
				if _, err := fmt.Fprintln(cmd.OutOrStdout(), string(jsonData)); err != nil {
					return fmt.Errorf("failed to write JSON output: %w", err)
				}
				return nil
			}

			return outputNetworkInfo(cmd, networkInfo)
		},
	}

	cmd.Flags().BoolVar(&externalIP, "external-ip", false, "Include external IP detection")
	cmd.Flags().BoolVarP(&jsonFormat, "json", "j", false, "Output in JSON format")

	return cmd
}

// Helper functions for subcommands

func collectSystemInfo() SystemInfo {
	hostname, _ := os.Hostname()
	wd, _ := os.Getwd()

	return SystemInfo{
		OS:           runtime.GOOS,
		Architecture: runtime.GOARCH,
		GoVersion:    runtime.Version(),
		NumCPU:       runtime.NumCPU(),
		Hostname:     hostname,
		WorkingDir:   wd,
		Timestamp:    time.Now(),
	}
}

func outputSystemInfo(cmd *cobra.Command, info SystemInfo) error {
	out := cmd.OutOrStdout()

	if err := writeFprintln(out, "🖥️  System Information"); err != nil {
		return fmt.Errorf("failed to write system info header: %w", err)
	}
	if err := writeFprintln(out, "=================================================="); err != nil {
		return fmt.Errorf("failed to write system info separator: %w", err)
	}
	if err := writeFprintf(out, "%-16s | %s\n", "OS", info.OS); err != nil {
		return fmt.Errorf("failed to write OS: %w", err)
	}
	if err := writeFprintf(out, "%-16s | %s\n", "Architecture", info.Architecture); err != nil {
		return fmt.Errorf("failed to write architecture: %w", err)
	}
	if err := writeFprintf(out, "%-16s | %s\n", "Go Version", info.GoVersion); err != nil {
		return fmt.Errorf("failed to write Go version: %w", err)
	}
	if err := writeFprintf(out, "%-16s | %d\n", "CPU Cores", info.NumCPU); err != nil {
		return fmt.Errorf("failed to write CPU cores: %w", err)
	}
	if err := writeFprintf(out, "%-16s | %s\n", "Hostname", info.Hostname); err != nil {
		return fmt.Errorf("failed to write hostname: %w", err)
	}
	if err := writeFprintf(out, "%-16s | %s\n", "Working Dir", info.WorkingDir); err != nil {
		return fmt.Errorf("failed to write working dir: %w", err)
	}
	if err := writeFprintf(out, "%-16s | %s\n", "Timestamp", info.Timestamp.Format(time.RFC3339)); err != nil {
		return fmt.Errorf("failed to write timestamp: %w", err)
	}

	return nil
}

func outputApplicationPaths(cmd *cobra.Command, paths ApplicationPaths) error {
	out := cmd.OutOrStdout()

	if err := writeFprintln(out, "🏠 Application Environment"); err != nil {
		return fmt.Errorf("failed to write app environment header: %w", err)
	}
	if err := writeFprintln(out, "=================================================="); err != nil {
		return fmt.Errorf("failed to write app environment separator: %w", err)
	}
	if err := writeFprintf(out, "%-16s | %s\n", "Home", paths.Home); err != nil {
		return fmt.Errorf("failed to write home path: %w", err)
	}
	if err := writeFprintf(out, "%-16s | %s\n", "WorkDir", paths.WorkDir); err != nil {
		return fmt.Errorf("failed to write workdir path: %w", err)
	}
	if err := writeFprintf(out, "%-16s | %s\n", "Cache", paths.Cache); err != nil {
		return fmt.Errorf("failed to write cache path: %w", err)
	}
	if err := writeFprintf(out, "%-16s | %s\n", "Logs", paths.Logs); err != nil {
		return fmt.Errorf("failed to write logs path: %w", err)
	}
	if err := writeFprintf(out, "%-16s | %s\n", "Configs", paths.Configs); err != nil {
		return fmt.Errorf("failed to write configs path: %w", err)
	}
	if err := writeFprintf(out, "%-16s | %s\n", "Temp", paths.Temp); err != nil {
		return fmt.Errorf("failed to write temp path: %w", err)
	}

	return nil
}

func collectEnvironmentVariables(all bool, filter string) map[string]string {
	variables := make(map[string]string)
	envVars := os.Environ()

	keyVariables := []string{"HOME", "USER", "PATH", "SHELL", "TERM", "LANG", "PWD"}
	// Add SUMPTER_ variables to key variables
	for name := range config.GetSumpterEnvVars() {
		keyVariables = append(keyVariables, name)
	}

	if all {
		for _, env := range envVars {
			pair := strings.SplitN(env, "=", 2)
			key := pair[0]
			value := ""
			if len(pair) > 1 {
				value = pair[1]
			}
			if filter == "" || strings.Contains(strings.ToLower(key), strings.ToLower(filter)) {
				variables[key] = value
			}
		}
	} else {
		for _, key := range keyVariables {
			value := os.Getenv(key)
			if value != "" {
				if filter == "" || strings.Contains(strings.ToLower(key), strings.ToLower(filter)) {
					variables[key] = value
				}
			}
		}
		if filter != "" {
			for _, env := range envVars {
				pair := strings.SplitN(env, "=", 2)
				key := pair[0]
				value := ""
				if len(pair) > 1 {
					value = pair[1]
				}
				if !contains(keyVariables, key) && strings.Contains(strings.ToLower(key), strings.ToLower(filter)) {
					variables[key] = value
				}
			}
		}
	}

	return variables
}

func outputEnvironmentVariables(cmd *cobra.Command, variables map[string]string) error {
	out := cmd.OutOrStdout()

	if err := writeFprintln(out, "🌍 Environment Variables"); err != nil {
		return fmt.Errorf("failed to write environment variables header: %w", err)
	}
	if err := writeFprintln(out, "=================================================="); err != nil {
		return fmt.Errorf("failed to write environment variables separator: %w", err)
	}

	if len(variables) == 0 {
		if err := writeFprintln(out, "No environment variables found matching the filter."); err != nil {
			return fmt.Errorf("failed to write no variables message: %w", err)
		}
		return nil
	}

	maxKeyLength := 20
	for key := range variables {
		if len(key) > maxKeyLength {
			maxKeyLength = len(key)
		}
	}

	for key, value := range variables {
		// Redact sensitive values
		value = maybeRedact(key, value)

		if len(value) > 50 {
			value = value[:50] + "..."
		}
		if err := writeFprintf(out, "%-*s | %s\n", maxKeyLength, key, value); err != nil {
			return fmt.Errorf("failed to write variable %s: %w", key, err)
		}
	}

	return nil
}

func collectXMLCapabilities() XMLCapabilities {
	return XMLCapabilities{
		StreamingSupported: true,
		Encodings:          append([]string(nil), xmlSupportedEncodings...),
		MaxMemoryTarget:    xmlInputMemoryTarget,
		SupportedOutputs:   append([]string(nil), xmlSupportedOutputs...),
	}
}

func outputXMLCapabilities(cmd *cobra.Command, capabilities XMLCapabilities) error {
	out := cmd.OutOrStdout()

	if err := writeFprintln(out, "📄 XML Processing Capabilities"); err != nil {
		return fmt.Errorf("failed to write XML capabilities header: %w", err)
	}
	if err := writeFprintln(out, "=================================================="); err != nil {
		return fmt.Errorf("failed to write XML capabilities separator: %w", err)
	}
	if err := writeFprintf(out, "%-16s | %t\n", "Streaming", capabilities.StreamingSupported); err != nil {
		return fmt.Errorf("failed to write streaming info: %w", err)
	}
	if err := writeFprintf(out, "%-16s | %s\n", "Memory Target", capabilities.MaxMemoryTarget); err != nil {
		return fmt.Errorf("failed to write memory target: %w", err)
	}
	if err := writeFprintf(out, "%-16s | %s\n", "Encodings", strings.Join(capabilities.Encodings, ", ")); err != nil {
		return fmt.Errorf("failed to write encodings: %w", err)
	}
	if err := writeFprintf(out, "%-16s | %s\n", "Outputs", strings.Join(capabilities.SupportedOutputs, ", ")); err != nil {
		return fmt.Errorf("failed to write outputs: %w", err)
	}

	return nil
}

func collectNetworkInfo(includeExternalIP bool) (*NetworkInfo, error) {
	var networkInfo NetworkInfo

	// Get network interfaces
	interfaces, err := getNetworkInterfaces()
	if err == nil {
		networkInfo.Interfaces = interfaces
	}

	// Get external IP if requested
	if includeExternalIP {
		if ip, err := getExternalIP(); err == nil {
			// We need to add this to the system info, but since we're in network subcommand,
			// we'll create a simple structure
			networkInfo.ExternalIP = ip
		}
	}

	return &networkInfo, nil
}

func outputNetworkInfo(cmd *cobra.Command, info *NetworkInfo) error {
	out := cmd.OutOrStdout()

	if err := writeFprintln(out, "🌐 Network Interfaces"); err != nil {
		return fmt.Errorf("failed to write network interfaces header: %w", err)
	}
	if err := writeFprintln(out, "=================================================="); err != nil {
		return fmt.Errorf("failed to write network interfaces separator: %w", err)
	}

	if len(info.Interfaces) == 0 {
		if err := writeFprintln(out, "No network interfaces found."); err != nil {
			return fmt.Errorf("failed to write no interfaces message: %w", err)
		}
	} else {
		for _, iface := range info.Interfaces {
			if err := writeFprintf(out, "%-20s | %s (%s)\n", iface.Name, iface.Address, iface.Status); err != nil {
				return fmt.Errorf("failed to write interface %s: %w", iface.Name, err)
			}
		}
	}

	if info.ExternalIP != "" {
		if err := writeFprintln(out, ""); err != nil {
			return fmt.Errorf("failed to write external IP newline: %w", err)
		}
		if err := writeFprintf(out, "%-16s | %s\n", "External IP", info.ExternalIP); err != nil {
			return fmt.Errorf("failed to write external IP: %w", err)
		}
	}

	return nil
}
