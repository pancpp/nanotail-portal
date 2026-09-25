package conf

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

func TestRejectRemovedDatabaseCommands(t *testing.T) {
	for _, args := range [][]string{{"db", "init"}, {"db", "migrate"}, {"db", "status"}, {"db", "create", "test"}, {"unexpected"}} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			path := isolateConfig(t)
			original := pflag.CommandLine
			pflag.CommandLine = pflag.NewFlagSet("test", pflag.ContinueOnError)
			t.Cleanup(func() { pflag.CommandLine = original })
			if err := pflag.CommandLine.Parse(args); err != nil {
				t.Fatal(err)
			}
			if err := Init(); err == nil || !strings.Contains(err.Error(), "unexpected arguments") {
				t.Fatalf("removed command was not rejected: %v", err)
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("rejected command created a configuration file: %v", err)
			}
		})
	}
}
