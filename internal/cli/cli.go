package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/governance"
	"github.com/iloveZzz/yss-cli/internal/governance/reading"
	"github.com/iloveZzz/yss-cli/internal/schema"
	"github.com/iloveZzz/yss-cli/internal/transaction"
	"github.com/iloveZzz/yss-cli/internal/worklayout"
	"io"
	"os"
	"strings"
)

type options struct {
	args       []string
	rawArgs    []string
	values     map[string]string
	json       bool
	duplicates []string
}

func isSemanticCommand(o options) bool {
	if len(o.args) < 2 {
		return false
	}
	group, action := o.args[0], o.args[1]
	if group == "lifecycle" && (action == "route" || action == "verify-daily" || action == "target") {
		return true
	}
	return action == "verify" && (group == "lifecycle" || group == "contract" || group == "evidence" || group == "handoff") || group == "project-ci" && (action == "check" || action == "verify") && o.values["scope"] != "native-go"
}
func semInputCode(code string) bool {
	switch code {
	case "ARGUMENT", "ROOT", "PATH", "IDENTITY", "LEGACY", "BASELINE", "UNPORTED", "CANCELLED", "CAPABILITY", "INPUT", "EXECUTION", "INTERNAL":
		return true
	}
	return false
}
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	return RunWithTerminal(ctx, args, stdout, stderr, writerIsTerminal)
}

// RunWithTerminal allows hosts to provide their terminal boundary without
// changing command execution, validation or machine output.
func RunWithTerminal(ctx context.Context, args []string, stdout, stderr io.Writer, terminal func(io.Writer) bool) int {
	inv := &invocation{o: options{values: map[string]string{}, rawArgs: append([]string{}, args...)}}
	root := newRootCommand(inv, stdout, stderr)
	root.SetContext(ctx)
	input, err := prepareInput(root, args)
	preflightFailed := err != nil
	inv.o.args = input.helpArgs
	if len(inv.o.args) > 0 {
		inv.command = inv.o.args[0]
	}
	if input.help {
		content, helpErr := renderHelpFrom(root, input.helpArgs)
		if helpErr != nil {
			fmt.Fprintln(stderr, "ARGUMENT: "+helpErr.Error())
			return 2
		}
		fmt.Fprintln(stdout, content)
		return 0
	}
	if preflightFailed || !input.completion && (len(input.helpArgs) == 0 || input.version) {
		parseArgs := input.args
		if preflightFailed {
			parseArgs = input.parsedPrefix
		}
		if parseErr := inputFlags(root).Parse(parseArgs); parseErr != nil {
			inv.parseFailed = true
			err = flagError(root, parseErr)
		}
		if err == nil {
			if inv.o.values["version"] != "true" {
				content, _ := renderHelpFrom(root, nil)
				fmt.Fprintln(stdout, content)
				return 0
			}
			err = inv.run(root, input.helpArgs, coreCommand)
		}
	} else if err == nil {
		if input.completion {
			inv.rawOutput = true
			root.SetErr(io.Discard)
		}
		root.SetArgs(input.args)
		_, err = root.ExecuteContextC(ctx)
	}
	if inv.rawOutput {
		if err != nil {
			fmt.Fprintln(stderr, strings.ToUpper(inv.command)+": "+err.Error())
			return 1
		}
		return 0
	}
	if !inv.executed && err != nil {
		inv.profile = inv.o.values["profile"]
		if inv.o.values["version"] == "true" {
			inv.command = "version"
		}
		inv.o.json = inv.o.values["json"] == "true"
		if preflightFailed || inv.parseFailed {
			inv.o.json = input.fallback.json
			for _, key := range []string{"human", "diagnostics"} {
				if value, ok := input.fallback.values[key]; ok {
					inv.o.values[key] = value
				}
			}
		}
	}
	return writeOutcome(ctx, stdout, stderr, terminal, inv, err)
}

func writeOutcome(ctx context.Context, stdout, stderr io.Writer, terminal func(io.Writer) bool, inv *invocation, err error) int {
	o, command := inv.o, inv.command
	human := !o.json && (o.values["human"] == "true" || terminal != nil && terminal(stdout))
	withDiagnostics := o.values["diagnostics"] == "true"
	inputError := !inv.executed
	delete(o.values, "help")
	delete(o.values, "version")
	delete(o.values, "human")
	delete(o.values, "diagnostics")
	profile, result := inv.profile, inv.result
	if err == nil {
		if o.json {
			_ = json.NewEncoder(stdout).Encode(domain.Envelope{OutputVersion: 1, Version: domain.Version, ProtocolVersion: domain.ProtocolVersion, Command: command, Profile: profile, Status: "ok", Code: "OK", Result: result})
		} else if command == "contract" && len(o.args) > 1 && o.args[1] == "view" {
			fmt.Fprintln(stdout, renderContractReading(result))
		} else if human {
			fmt.Fprintln(stdout, renderHumanSuccess(command, o, profile, result))
		} else {
			b, _ := json.MarshalIndent(result, "", "  ")
			fmt.Fprintln(stdout, string(b))
		}
		return 0
	}
	code := "INTERNAL"
	exit := 1
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		code = "CANCELLED"
	}
	var de *domain.Error
	if errors.As(err, &de) {
		code = de.Code
		if de.Exit != 0 {
			exit = de.Exit
		}
	}
	if isSemanticCommand(o) && semInputCode(code) {
		exit = 2
	}
	var se *schema.Error
	if de == nil && errors.As(err, &se) {
		code = se.Code
	}
	receipt, hasReceipt := result.(transaction.Result)
	result = map[string]any{"message": err.Error()}
	var reported interface{ ErrorResult() any }
	if errors.As(err, &reported) {
		result = reported.ErrorResult()
	} else if isSemanticCommand(o) && !inputError {
		result = governance.InputFailureReport(ctx, o.values["root"], command, err)
	}
	if hasReceipt {
		if receipt.FileApplication == "" {
			receipt.FileApplication = "not-applied"
		}
		if receipt.Verification == "" {
			receipt.Verification = "not-run"
		}
		result = map[string]any{"message": err.Error(), "receipt": receipt, "fileApplication": receipt.FileApplication, "verification": receipt.Verification}
	}
	if code == "ARGUMENT" {
		exit = 2
		message := err.Error()
		if !strings.Contains(message, "--help") {
			message += "；查看 " + helpCommand(o.args)
		}
		if inputError || !isSemanticCommand(o) {
			result = map[string]any{"message": message}
		}
		err = domain.WithMessage(err, message)
	}
	diagnostic := diagnosticFor(code, err, o, profile, result)
	if o.json {
		env := domain.Envelope{OutputVersion: 1, Version: domain.Version, ProtocolVersion: domain.ProtocolVersion, Command: command, Profile: profile, Status: "error", Code: code, Result: result}
		if withDiagnostics {
			env.Diagnostic = diagnostic
		}
		_ = json.NewEncoder(stdout).Encode(env)
	} else if human {
		fmt.Fprintln(stderr, renderHumanDiagnostic(diagnostic))
	} else {
		if isSemanticCommand(o) && !inputError {
			b, _ := json.MarshalIndent(result, "", "  ")
			fmt.Fprintln(stdout, string(b))
		}
		fmt.Fprintln(stderr, code+": "+err.Error())
	}
	return exit
}
func capabilities() map[string]any {
	m := capabilitiesBase()
	m["nodeDelegation"] = nodeDelegation
	m["scaffold"] = map[string]any{"architectures": []string{"ddd", "mvc"}, "runtime": "go", "source": "embedded-spec-bundle", "initializeOnly": true, "buildExecuted": false}
	return m
}
func capabilitiesBase() map[string]any {
	return map[string]any{"readingViews": map[string]any{"schemaVersion": 1, "managed": map[string]any{"commands": []string{"render", "check-views", "plan-enable", "apply-enable", "affected"}, "manifestSchemaVersions": map[string]any{"read": []int{1, 2}, "write": []int{1, 2}}, "rulesVersion": reading.RulesVersion, "rendererId": reading.RendererID}, "contract.view": []string{"slice:review", "slice:task", "slice:full"}, "context.query": []string{"agent"}, "lifecycle.query": []string{"agent"}, "lifecycle.status": []string{"agent"}, "lifecycle.route": []string{"agent"}}, "workLayout": map[string]any{"schemaVersion": 1, "rootAuthority": worklayout.TrackerRef + "#tracker.root", "newProjectDefault": worklayout.DefaultRoot, "preserveExistingConfig": true}, "releaseQualification": "external-release-manifest", "version": domain.Version, "native": []string{"identity", "fixed-offline-bundles", "init", "attach-plan-and-apply", "diff", "sync-plan-and-apply", "transaction-recover", "contextual-help", "work-layout-v1", "offline-tutorial", "online-program-upgrade", "offline-program-update", "program-update-recover-and-rollback", "schema", "strict-yaml", "context", "lifecycle-query", "lifecycle-target-v1", "profile-guidance-v1", "profile-prepare", "git-workflow-v1", "spec-baseline-v1", "stage-register-and-update", "scoped-project-ci", "runtime-basic-records", "runtime-record-queries-and-pins", "safe-zip-and-xml", "mcp-stdio-read-only-v1", "tracker-github-mirror-v1", "trace-matrix-v1", "runtime-input-objects-and-report-pin-v1"}, "governanceCandidate": map[string]any{"status": "implemented", "targetVersion": domain.Version, "readOnly": true, "approval_created": false, "defaultCIScope": "complete-governance", "runtimeStore": []string{"off"}, "interfaces": []string{"lifecycle.target", "lifecycle.status", "lifecycle.route", "lifecycle.route:prepare-inputs", "lifecycle.verify-daily", "lifecycle.verify", "contract.verify:slice,scaffold,task,business-tickets", "evidence.verify:approval,user-decision,verification", "handoff.verify:package,consumption,spec-baseline", "project-ci.check", "project-ci.verify"}, "exitCodes": map[string]int{"passed": 0, "rejected": 1, "inputCapabilityExecution": 2}}, "requiredReleaseEvidence": []string{"plugin-consumer-cutover-verification", "native-declared-release-platform-validation", "fixed-source-release-gate"}, "legacyRuntimeRetained": false}
}

var _ = os.ErrNotExist
