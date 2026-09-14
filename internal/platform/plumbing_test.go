package platform

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every TOMB_* variable the application reads has to travel five hops to reach
// the container: a tofu variable, an instance metadata key, a line in
// deploy/configure.sh that reads the metadata, a line that writes it to .env,
// and an entry in deploy/compose.yaml that passes it in. Miss one and the
// variable is documented, settable, and does nothing -- which is how
// TOMB_GUILD_RANKS shipped: the docs said `gh variable set` and the value never
// left GitHub.
//
// This reads the actual files and holds the hops to agree. It is the one test
// in the package that looks outside internal/, and it is worth being that.

var envRead = regexp.MustCompile(`os\.Getenv\("(TOMB_[A-Z_]+)"\)|envOr\("(TOMB_[A-Z_]+)"`)

func TestEveryTombVariableIsPlumbed(t *testing.T) {
	root := filepath.Join("..", "..")
	read := func(rel string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Skipf("cannot read %s: %v (not in a checkout?)", rel, err)
		}
		return string(b)
	}

	config := read(filepath.Join("internal", "platform", "config.go"))
	compose := read(filepath.Join("deploy", "compose.yaml"))
	configure := read(filepath.Join("deploy", "configure.sh"))
	compute := read(filepath.Join("tofu", "compute.tf"))
	variables := read(filepath.Join("tofu", "variables.tf"))

	var envs []string
	seen := map[string]bool{}
	for _, m := range envRead.FindAllStringSubmatch(config, -1) {
		name := m[1] + m[2]
		if !seen[name] {
			seen[name] = true
			envs = append(envs, name)
		}
	}
	if len(envs) < 4 {
		t.Fatalf("found only %v read from the environment in config.go; the regexp is missing something", envs)
	}

	for _, env := range envs {
		// TOMB_GUILD_RANKS -> tomb-guild-ranks -> guild_ranks
		key := strings.ToLower(strings.ReplaceAll(env, "_", "-"))
		tfvar := strings.TrimPrefix(strings.ToLower(env), "tomb_")

		if !strings.Contains(compose, env+": ${"+env+":") {
			t.Errorf("%s: deploy/compose.yaml does not pass it to the app", env)
		}
		if !strings.Contains(configure, env+`="$(meta `+key+`)"`) {
			t.Errorf("%s: deploy/configure.sh does not read metadata %s", env, key)
		}
		if !strings.Contains(configure, `echo "`+env+`=$`+env+`"`) {
			t.Errorf("%s: deploy/configure.sh does not write it to .env", env)
		}
		if !regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(key) + `\s*=\s*var\.` + regexp.QuoteMeta(tfvar) + `\b`).MatchString(compute) {
			t.Errorf("%s: tofu/compute.tf has no metadata %s = var.%s", env, key, tfvar)
		}
		if !strings.Contains(variables, `variable "`+tfvar+`"`) {
			t.Errorf("%s: tofu/variables.tf declares no variable %q", env, tfvar)
		}
	}
}
