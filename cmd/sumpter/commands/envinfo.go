package commands

import (
	"encoding/json"
	"fmt"
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
		fmt.Fprintln(cmd.OutOrStdout(), string(jsonData))
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
		xmlCapabilities = &XMLCapabilities{
			StreamingSupported: true,
			Encodings:          []string{"UTF-8", "UTF-16", "ISO-8859-1", "Windows-1252"},
			MaxMemoryTarget:    "<50MB RSS",
			SupportedOutputs:   []string{"NDJSON", "Parquet", "DuckDB", "Markdown"},
		}
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
	defer resp.Body.Close()

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
	fmt.Fprintln(out, "🖥️  System Information")
	fmt.Fprintln(out, "==================================================")
	fmt.Fprintf(out, "%-16s | %s\n", "OS", data.System.OS)
	fmt.Fprintf(out, "%-16s | %s\n", "Architecture", data.System.Architecture)
	fmt.Fprintf(out, "%-16s | %s\n", "Go Version", data.System.GoVersion)
	fmt.Fprintf(out, "%-16s | %d\n", "CPU Cores", data.System.NumCPU)
	fmt.Fprintf(out, "%-16s | %s\n", "Hostname", data.System.Hostname)
	fmt.Fprintf(out, "%-16s | %s\n", "Working Dir", data.System.WorkingDir)
	fmt.Fprintf(out, "%-16s | %s\n", "Timestamp", data.System.Timestamp.Format(time.RFC3339))
	if data.System.ExternalIP != "" {
		fmt.Fprintf(out, "%-16s | %s\n", "External IP", data.System.ExternalIP)
	}
	fmt.Fprintln(out, "")

	// XML Capabilities Section
	if data.XML.StreamingSupported {
		fmt.Fprintln(out, "📄 XML Processing Capabilities")
		fmt.Fprintln(out, "==================================================")
		fmt.Fprintf(out, "%-16s | %t\n", "Streaming", data.XML.StreamingSupported)
		fmt.Fprintf(out, "%-16s | %s\n", "Memory Target", data.XML.MaxMemoryTarget)
		fmt.Fprintf(out, "%-16s | %s\n", "Encodings", strings.Join(data.XML.Encodings, ", "))
		fmt.Fprintf(out, "%-16s | %s\n", "Outputs", strings.Join(data.XML.SupportedOutputs, ", "))
		fmt.Fprintln(out, "")
	}

	// Application Environment Section
	fmt.Fprintln(out, "🏠 Application Environment")
	fmt.Fprintln(out, "==================================================")
	fmt.Fprintf(out, "%-16s | %s\n", "Home", data.Application.Home)
	fmt.Fprintf(out, "%-16s | %s\n", "WorkDir", data.Application.WorkDir)
	fmt.Fprintf(out, "%-16s | %s\n", "Cache", data.Application.Cache)
	fmt.Fprintf(out, "%-16s | %s\n", "Logs", data.Application.Logs)
	fmt.Fprintf(out, "%-16s | %s\n", "Configs", data.Application.Configs)
	fmt.Fprintf(out, "%-16s | %s\n", "Temp", data.Application.Temp)
	fmt.Fprintln(out, "")

	// Network Interfaces Section
	if len(data.Network.Interfaces) > 0 {
		fmt.Fprintln(out, "🌐 Network Interfaces")
		fmt.Fprintln(out, "==================================================")
		for _, iface := range data.Network.Interfaces {
			fmt.Fprintf(out, "%-20s | %s (%s)\n", iface.Name, iface.Address, iface.Status)
		}
		fmt.Fprintln(out, "")
	}

	// Environment Variables Section
	fmt.Fprintln(out, "🌍 Environment Variables")
	fmt.Fprintln(out, "==================================================")

	if len(data.Variables) == 0 {
		fmt.Fprintln(out, "No environment variables found matching the filter.")
	} else {
		if exportFormat {
			for key, value := range data.Variables {
				// Redact sensitive values
				value = maybeRedact(key, value)
				fmt.Fprintf(out, "export %s=%q\n", key, value)
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
				fmt.Fprintf(out, "%-*s | %s\n", maxKeyLength, key, value)
			}
		}
	}

	// Stats
	if !exportFormat {
		fmt.Fprintln(out, "")
		fmt.Fprintln(out, "📊 Stats")
		fmt.Fprintln(out, "==================================================")
		fmt.Fprintf(out, "%-16s | %d\n", "Total Vars", data.Stats.TotalVars)
		fmt.Fprintf(out, "%-16s | %d\n", "Filtered Vars", data.Stats.FilteredVars)
		fmt.Fprintf(out, "%-16s | %d\n", "Key Vars", data.Stats.KeyVarsCount)
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
				fmt.Fprintln(cmd.OutOrStdout(), string(jsonData))
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
				fmt.Fprintln(cmd.OutOrStdout(), string(jsonData))
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
				fmt.Fprintln(cmd.OutOrStdout(), string(jsonData))
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
				fmt.Fprintln(cmd.OutOrStdout(), string(jsonData))
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
				fmt.Fprintln(cmd.OutOrStdout(), string(jsonData))
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

	fmt.Fprintln(out, "🖥️  System Information")
	fmt.Fprintln(out, "==================================================")
	fmt.Fprintf(out, "%-16s | %s\n", "OS", info.OS)
	fmt.Fprintf(out, "%-16s | %s\n", "Architecture", info.Architecture)
	fmt.Fprintf(out, "%-16s | %s\n", "Go Version", info.GoVersion)
	fmt.Fprintf(out, "%-16s | %d\n", "CPU Cores", info.NumCPU)
	fmt.Fprintf(out, "%-16s | %s\n", "Hostname", info.Hostname)
	fmt.Fprintf(out, "%-16s | %s\n", "Working Dir", info.WorkingDir)
	fmt.Fprintf(out, "%-16s | %s\n", "Timestamp", info.Timestamp.Format(time.RFC3339))

	return nil
}

func outputApplicationPaths(cmd *cobra.Command, paths ApplicationPaths) error {
	out := cmd.OutOrStdout()

	fmt.Fprintln(out, "🏠 Application Environment")
	fmt.Fprintln(out, "==================================================")
	fmt.Fprintf(out, "%-16s | %s\n", "Home", paths.Home)
	fmt.Fprintf(out, "%-16s | %s\n", "WorkDir", paths.WorkDir)
	fmt.Fprintf(out, "%-16s | %s\n", "Cache", paths.Cache)
	fmt.Fprintf(out, "%-16s | %s\n", "Logs", paths.Logs)
	fmt.Fprintf(out, "%-16s | %s\n", "Configs", paths.Configs)
	fmt.Fprintf(out, "%-16s | %s\n", "Temp", paths.Temp)

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

	fmt.Fprintln(out, "🌍 Environment Variables")
	fmt.Fprintln(out, "==================================================")

	if len(variables) == 0 {
		fmt.Fprintln(out, "No environment variables found matching the filter.")
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
		fmt.Fprintf(out, "%-*s | %s\n", maxKeyLength, key, value)
	}

	return nil
}

func collectXMLCapabilities() XMLCapabilities {
	return XMLCapabilities{
		StreamingSupported: true,
		Encodings:          []string{"UTF-8", "UTF-16", "ISO-8859-1", "Windows-1252"},
		MaxMemoryTarget:    "<50MB RSS",
		SupportedOutputs:   []string{"NDJSON", "Parquet", "DuckDB", "Markdown"},
	}
}

func outputXMLCapabilities(cmd *cobra.Command, capabilities XMLCapabilities) error {
	out := cmd.OutOrStdout()

	fmt.Fprintln(out, "📄 XML Processing Capabilities")
	fmt.Fprintln(out, "==================================================")
	fmt.Fprintf(out, "%-16s | %t\n", "Streaming", capabilities.StreamingSupported)
	fmt.Fprintf(out, "%-16s | %s\n", "Memory Target", capabilities.MaxMemoryTarget)
	fmt.Fprintf(out, "%-16s | %s\n", "Encodings", strings.Join(capabilities.Encodings, ", "))
	fmt.Fprintf(out, "%-16s | %s\n", "Outputs", strings.Join(capabilities.SupportedOutputs, ", "))

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

	fmt.Fprintln(out, "🌐 Network Interfaces")
	fmt.Fprintln(out, "==================================================")

	if len(info.Interfaces) == 0 {
		fmt.Fprintln(out, "No network interfaces found.")
	} else {
		for _, iface := range info.Interfaces {
			fmt.Fprintf(out, "%-20s | %s (%s)\n", iface.Name, iface.Address, iface.Status)
		}
	}

	if info.ExternalIP != "" {
		fmt.Fprintln(out, "")
		fmt.Fprintf(out, "%-16s | %s\n", "External IP", info.ExternalIP)
	}

	return nil
}
