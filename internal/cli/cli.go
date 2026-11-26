package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/agent-fault-check/afc/internal/config"
	"github.com/agent-fault-check/afc/internal/license"
	"github.com/agent-fault-check/afc/internal/report"
	"github.com/agent-fault-check/afc/internal/runner"
)

var (
	Version = "0.1.0-dev"
	Commit  = "local"
)

// Run executes the afc CLI and returns the process exit code (0, 1, or 2).
func Run(args []string) int {
	if len(args) == 0 {
		printUsage()
		return 2
	}

	switch args[0] {
	case "version":
		fmt.Printf("afc %s (commit %s, supported spec: 1.0)\n", Version, Commit)
		return 0
	case "validate":
		return cmdValidate(args[1:])
	case "activate":
		return cmdActivate(args[1:])
	case "run":
		return cmdRun(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", args[0])
		printUsage()
		return 2
	}
}

func cmdValidate(args []string) int {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	specPath := fs.String("spec", "", "Path to chaos-spec.yaml")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "error: unexpected positional arguments: %v\n", fs.Args())
		return 2
	}
	if *specPath == "" {
		fmt.Fprintln(os.Stderr, "error: --spec is required")
		return 2
	}
	if _, err := config.LoadFile(*specPath); err != nil {
		fmt.Fprintf(os.Stderr, "validation failed: %v\n", err)
		return 2
	}
	fmt.Printf("valid: %s\n", *specPath)
	return 0
}

func cmdActivate(args []string) int {
	fs := flag.NewFlagSet("activate", flag.ContinueOnError)
	licenseFlag := fs.String("license-key", "", "Polar license key")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "error: unexpected positional arguments: %v\n", fs.Args())
		return 2
	}
	key := license.ResolveKey(*licenseFlag)
	tier, err := license.Activate(context.Background(), key)
	if err != nil {
		fmt.Fprintf(os.Stderr, "activation failed: %v\n", err)
		return 2
	}
	fmt.Printf("activated tier: %s\n", tier)
	return 0
}

func cmdRun(args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	specPath := fs.String("spec", "", "Path to chaos-spec.yaml")
	scenarioID := fs.String("scenario", "", "Run only the specified scenario ID")
	licenseFlag := fs.String("license-key", "", "Polar license key")
	sarifPath := fs.String("sarif", "", "Write SARIF 2.1.0 report to path")
	junitPath := fs.String("junit", "", "Write JUnit XML report to path")
	verbose := fs.Bool("verbose", false, "Print detailed assertion results")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "error: unexpected positional arguments: %v\n", fs.Args())
		return 2
	}
	if *specPath == "" {
		fmt.Fprintln(os.Stderr, "error: --spec is required")
		return 2
	}

	spec, err := config.LoadFile(*specPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		return 2
	}

	if *scenarioID != "" {
		found := false
		for _, sc := range spec.Scenarios {
			if sc.ID == *scenarioID {
				found = true
				break
			}
		}
		if !found {
			fmt.Fprintf(os.Stderr, "config error: scenario %q not found in %s\n", *scenarioID, *specPath)
			return 2
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	key := license.ResolveKey(*licenseFlag)
	if _, err := license.Preflight(ctx, spec, *scenarioID, *sarifPath, *junitPath, key); err != nil {
		fmt.Fprintf(os.Stderr, "entitlement error: %v\n", err)
		return 2
	}

	res := runner.RunSuite(ctx, spec, *specPath, *scenarioID)
	res.ToolVersion = Version
	report.WriteConsole(os.Stdout, res, *verbose)

	return writeRequestedReports(res, *sarifPath, *junitPath)
}

func writeRequestedReports(res runner.SuiteResult, sarifPath, junitPath string) int {
	exitCode := res.ExitCode
	for _, output := range []struct{ format, path string }{{"sarif", sarifPath}, {"junit", junitPath}} {
		if output.path == "" {
			continue
		}
		if err := report.WriteProReport(output.format, res, output.path); err != nil {
			fmt.Fprintf(os.Stderr, "report write error (%s): %v\n", output.format, err)
			exitCode = 2
		}
	}
	return exitCode
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "Usage: afc <validate|run|activate|version> [flags]")
}
