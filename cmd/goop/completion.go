package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/TanguyBaudoin/goop/internal/bucket"
	"github.com/TanguyBaudoin/goop/internal/installer"
	"github.com/TanguyBaudoin/goop/internal/mavenrepo"
	"github.com/TanguyBaudoin/goop/internal/profile"
	"github.com/TanguyBaudoin/goop/internal/ui"
)

// topLevelCommands is the single source of truth for goop's top-level
// commands -- both scripts below get the list injected at generation
// time (%s placeholder) rather than hardcoding a second copy that could
// drift from run()'s switch in main.go.
var topLevelCommands = []string{
	"bootstrap", "install", "uninstall", "update", "list", "info", "search", "depends", "cleanup", "download", "reset", "hold", "unhold", "cache", "bucket", "maven-repo",
	"import", "migrate", "lock", "snapshot", "sync", "status", "profile", "why", "auth", "verify",
	"config", "completion", "index", "self-update", "version", "help",
}

const completionUsage = "usage: goop completion <powershell|bash> [--install]"

func cmdCompletion(args []string) int {
	if len(args) == 0 || len(args) > 2 {
		fmt.Fprintln(os.Stderr, completionUsage)
		return 2
	}
	install := false
	if len(args) == 2 {
		if args[1] != "--install" {
			fmt.Fprintln(os.Stderr, completionUsage)
			return 2
		}
		install = true
	}
	switch args[0] {
	case "powershell", "pwsh":
		if install {
			return installCompletion("powershell")
		}
		fmt.Printf(powershellCompletionScript, powershellQuotedList(topLevelCommands))
		return 0
	case "bash":
		if install {
			return installCompletion("bash")
		}
		fmt.Printf(bashCompletionScript, strings.Join(topLevelCommands, " "))
		return 0
	// Undocumented, machine-readable helpers the scripts above shell back
	// out to for dynamic completions (installed apps, available apps,
	// configured buckets) -- one plain name per line, no color/table
	// formatting, so they're trivial for a shell completer to consume.
	case "__apps":
		printLines(installedAppNames())
		return 0
	case "__available":
		printLines(availableAppNames())
		return 0
	case "__buckets":
		printLines(bucketNames())
		return 0
	case "__mavenrepos":
		printLines(mavenRepoNames())
		return 0
	case "__profiles":
		printLines(profileNames())
		return 0
	default:
		fmt.Fprintln(os.Stderr, completionUsage)
		return 2
	}
}

// completionLoadLine is what `--install` appends to a shell's startup
// file: the shell re-generates the completer from whatever goop binary
// is on PATH at startup, so it never goes stale as commands are added
// (unlike pasting the generated script itself into the profile).
func completionLoadLine(shell string) string {
	if shell == "bash" {
		return `eval "$(goop completion bash)"`
	}
	return "goop completion powershell | Out-String | Invoke-Expression"
}

// shellStartupFile returns the file --install appends to: PowerShell's
// $PROFILE (asked of PowerShell itself rather than guessed, since the
// path differs between Windows PowerShell and pwsh), or ~/.bashrc.
func shellStartupFile(shell string) (string, error) {
	if shell == "bash" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".bashrc"), nil
	}
	for _, exe := range []string{"pwsh.exe", "powershell.exe"} {
		if _, err := exec.LookPath(exe); err != nil {
			continue
		}
		out, err := exec.Command(exe, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", "$PROFILE").Output()
		if err != nil {
			continue
		}
		if p := strings.TrimSpace(string(out)); p != "" {
			return p, nil
		}
	}
	return "", fmt.Errorf("couldn't determine the PowerShell profile path (no pwsh.exe or powershell.exe on PATH)")
}

// installCompletion appends the loader line to the shell's startup
// file, creating it if needed. Idempotent: an existing line is left
// alone rather than duplicated, so re-running is harmless.
func installCompletion(shell string) int {
	path, err := shellStartupFile(shell)
	if err != nil {
		ui.Fail("completion --install: %v", err)
		return 1
	}
	line := completionLoadLine(shell)

	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		ui.Fail("completion --install: read %s: %v", path, err)
		return 1
	}
	if strings.Contains(string(existing), line) {
		ui.Ok("completion already registered in %s", path)
		return 0
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		ui.Fail("completion --install: %v", err)
		return 1
	}
	// Append, never rewrite: the file is the user's own, and may hold
	// unrelated setup that must survive untouched.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		ui.Fail("completion --install: open %s: %v", path, err)
		return 1
	}
	defer f.Close()

	block := "\n# goop shell completion\n" + line + "\n"
	if len(existing) == 0 {
		block = strings.TrimPrefix(block, "\n")
	}
	if _, err := f.WriteString(block); err != nil {
		ui.Fail("completion --install: write %s: %v", path, err)
		return 1
	}

	ui.Ok("completion registered in %s", path)
	fmt.Println(ui.Dim("open a new shell (or reload the file) for it to take effect"))
	return 0
}

func powershellQuotedList(items []string) string {
	quoted := make([]string, len(items))
	for i, s := range items {
		quoted[i] = "'" + s + "'"
	}
	return strings.Join(quoted, ",")
}

func printLines(names []string) {
	for _, n := range names {
		fmt.Println(n)
	}
}

func installedAppNames() []string {
	recs, err := installer.List()
	if err != nil {
		return nil
	}
	names := make([]string, len(recs))
	for i, r := range recs {
		names[i] = r.Name
	}
	return names
}

func bucketNames() []string {
	entries, err := bucket.List()
	if err != nil {
		return nil
	}
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name
	}
	return names
}

func mavenRepoNames() []string {
	entries, err := mavenrepo.List()
	if err != nil {
		return nil
	}
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name
	}
	return names
}

func profileNames() []string {
	names, err := profile.List()
	if err != nil {
		return nil
	}
	return names
}

// availableAppNames lists every app installable via `goop install`,
// bare and bucket-qualified (e.g. both "jq" and "main/jq") so either
// form completes -- drawn from a plain manifest-filename listing per
// bucket (bucket.ManifestNames), not a full decode, so it stays fast
// enough to run on every keystroke even against the real corpus (~4000
// manifests across main+extras).
func availableAppNames() []string {
	entries, err := bucket.List()
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		manifestNames, err := bucket.ManifestNames(e.Name)
		if err != nil {
			continue
		}
		for _, m := range manifestNames {
			names = append(names, m, e.Name+"/"+m)
		}
	}
	sort.Strings(names)
	return names
}

const powershellCompletionScript = `# goop shell completion for PowerShell.
# Load in your profile: goop completion powershell | Out-String | Invoke-Expression
Register-ArgumentCompleter -Native -CommandName 'goop' -ScriptBlock {
    param($wordToComplete, $commandAst, $cursorPosition)

    $tokens = $commandAst.CommandElements | ForEach-Object { $_.ToString() }
    # $n is how many *settled* tokens precede the word being completed --
    # CommandElements includes the word currently being typed once it's
    # non-empty (so "goop unin<tab>" has tokens=('goop','unin'), n=1), but
    # NOT when it's still empty (so "goop uninstall <tab>" also has
    # tokens=('goop','uninstall'), and n must still come out to 2, not 1,
    # since that empty word doesn't add an element). Drop the in-progress
    # element from the count so both cases land on the same $n.
    $n = $tokens.Count
    if ($wordToComplete -ne '' -and $n -gt 0 -and $tokens[$n-1] -eq $wordToComplete) {
        $n--
    }

    function Complete([string[]]$options) {
        $options | Where-Object { $_ -and $_ -like "$wordToComplete*" } | Sort-Object -Unique | ForEach-Object {
            [System.Management.Automation.CompletionResult]::new($_, $_, 'ParameterValue', $_)
        }
    }

    if ($n -le 1) {
        return Complete @(%s)
    }

    $sub = $tokens[1]
    switch ($sub) {
        { $_ -in 'uninstall','update' } {
            if ($n -ge 2) { return Complete (& goop completion __apps 2>$null) }
        }
        'cache' {
            if ($n -eq 2) { return Complete @('show','rm') }
        }
        { $_ -in 'hold','unhold' } {
            if ($n -ge 2) { return Complete (& goop completion __apps 2>$null) }
        }
        'reset' {
            if ($n -ge 2) { return Complete (& goop completion __apps 2>$null) }
        }
        'cleanup' {
            if ($n -eq 2) { return Complete (& goop completion __apps 2>$null) }
        }
        'info' {
            if ($n -eq 2) { return Complete (& goop completion __apps 2>$null) }
        }
        'download' {
            if ($n -ge 2) { return Complete (& goop completion __available 2>$null) }
        }
        'install' {
            if ($n -ge 2) { return Complete (& goop completion __available 2>$null) }
        }
        'depends' {
            if ($n -eq 2) { return Complete (& goop completion __available 2>$null) }
        }
        'bucket' {
            if ($n -eq 2) { return Complete @('add','list','remove','priority','update') }
            if ($n -eq 3 -and $tokens[2] -in @('remove','update','priority')) { return Complete (& goop completion __buckets 2>$null) }
        }
        'maven-repo' {
            if ($n -eq 2) { return Complete @('add','list','remove') }
            if ($n -eq 3 -and $tokens[2] -eq 'remove') { return Complete (& goop completion __mavenrepos 2>$null) }
        }
        'profile' {
            if ($n -eq 2) { return Complete @('use','list','show','add','remove','reset') }
            if ($n -eq 3 -and $tokens[2] -in 'use','add','remove') { return Complete (& goop completion __profiles 2>$null) }
            if ($n -eq 4 -and $tokens[2] -in 'add','remove') { return Complete (& goop completion __apps 2>$null) }
        }
        'why' {
            if ($n -eq 2) { return Complete (& goop completion __apps 2>$null) }
        }
        'auth' {
            if ($n -eq 2) { return Complete @('add','remove','list') }
        }
        'config' {
            if ($n -eq 2) { return Complete @('get-root','set-root','unset-root','get-proxy','set-proxy','unset-proxy','set-no-proxy','unset-no-proxy') }
        }
        'completion' {
            if ($n -eq 2) { return Complete @('powershell','bash') }
        }
    }
}
`

const bashCompletionScript = `# goop shell completion for bash (e.g. Git Bash).
# Load in ~/.bashrc: eval "$(goop completion bash)"
_goop_completions() {
    local cur="${COMP_WORDS[COMP_CWORD]}"
    local top="%s"

    if [ "$COMP_CWORD" -eq 1 ]; then
        COMPREPLY=( $(compgen -W "$top" -- "$cur") )
        return
    fi

    local sub="${COMP_WORDS[1]}"
    case "$sub" in
        uninstall|update|info|cleanup|reset|hold|unhold)
            COMPREPLY=( $(compgen -W "$(goop completion __apps 2>/dev/null)" -- "$cur") )
            ;;
        install|depends|download)
            COMPREPLY=( $(compgen -W "$(goop completion __available 2>/dev/null)" -- "$cur") )
            ;;
        bucket)
            if [ "$COMP_CWORD" -eq 2 ]; then
                COMPREPLY=( $(compgen -W "add list remove priority update" -- "$cur") )
            elif [ "${COMP_WORDS[2]}" = "update" -o "${COMP_WORDS[2]}" = "remove" -o "${COMP_WORDS[2]}" = "priority" ] && [ "$COMP_CWORD" -eq 3 ]; then
                COMPREPLY=( $(compgen -W "$(goop completion __buckets 2>/dev/null)" -- "$cur") )
            fi
            ;;
        maven-repo)
            if [ "$COMP_CWORD" -eq 2 ]; then
                COMPREPLY=( $(compgen -W "add list remove" -- "$cur") )
            elif [ "${COMP_WORDS[2]}" = "remove" ] && [ "$COMP_CWORD" -eq 3 ]; then
                COMPREPLY=( $(compgen -W "$(goop completion __mavenrepos 2>/dev/null)" -- "$cur") )
            fi
            ;;
        profile)
            if [ "$COMP_CWORD" -eq 2 ]; then
                COMPREPLY=( $(compgen -W "use list show add remove reset" -- "$cur") )
            elif [ "$COMP_CWORD" -eq 3 ] && { [ "${COMP_WORDS[2]}" = "use" ] || [ "${COMP_WORDS[2]}" = "add" ] || [ "${COMP_WORDS[2]}" = "remove" ]; }; then
                COMPREPLY=( $(compgen -W "$(goop completion __profiles 2>/dev/null)" -- "$cur") )
            elif [ "$COMP_CWORD" -eq 4 ] && { [ "${COMP_WORDS[2]}" = "add" ] || [ "${COMP_WORDS[2]}" = "remove" ]; }; then
                COMPREPLY=( $(compgen -W "$(goop completion __apps 2>/dev/null)" -- "$cur") )
            fi
            ;;
        why)
            COMPREPLY=( $(compgen -W "$(goop completion __apps 2>/dev/null)" -- "$cur") )
            ;;
        auth)
            [ "$COMP_CWORD" -eq 2 ] && COMPREPLY=( $(compgen -W "add remove list" -- "$cur") )
            ;;
        config)
            [ "$COMP_CWORD" -eq 2 ] && COMPREPLY=( $(compgen -W "get-root set-root unset-root get-proxy set-proxy unset-proxy set-no-proxy unset-no-proxy" -- "$cur") )
            ;;
        completion)
            [ "$COMP_CWORD" -eq 2 ] && COMPREPLY=( $(compgen -W "powershell bash" -- "$cur") )
            ;;
    esac
}
complete -F _goop_completions goop
`
