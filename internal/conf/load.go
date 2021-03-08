package conf

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/adrg/xdg"
	"github.com/release-engineering/exodus-rsync/internal/args"
	"github.com/release-engineering/exodus-rsync/internal/log"
	"gopkg.in/yaml.v3"
)

func candidatePaths() []string {
	return []string{
		"exodus-rsync.conf",
		xdg.ConfigHome + "/exodus-rsync.conf",
		"/etc/exodus-rsync.conf",
	}
}

func loadFromPath(path string) (*globalConfig, error) {
	file, err := os.Open(path)
	if err != nil {
		return &globalConfig{}, err
	}
	defer file.Close()

	defaults := sharedConfig{GwPollIntervalRaw: 5000}

	dec := yaml.NewDecoder(file)
	out := &globalConfig{}
	out.sharedConfig = defaults

	err = dec.Decode(&out)
	if err != nil {
		return &globalConfig{}, fmt.Errorf("can't parse %s: %w", path, err)
	}

	// A bit of normalization...
	for {
		if !strings.HasSuffix(out.GwURLRaw, "/") {
			break
		}
		out.GwURLRaw = strings.TrimSuffix(out.GwURLRaw, "/")
	}

	// A few vars support env var expansion for convenience
	out.GwCertRaw = os.ExpandEnv(out.GwCertRaw)
	out.GwKeyRaw = os.ExpandEnv(out.GwKeyRaw)

	// Fill in the Environment parent references
	prefs := map[string]bool{}
	for i := range out.EnvironmentsRaw {
		env := &out.EnvironmentsRaw[i]
		if prefs[env.Prefix()] {
			return nil, fmt.Errorf("duplicate environment definitions for '%s'", env.Prefix())
		}
		prefs[env.Prefix()] = true
		out.EnvironmentsRaw[i].parent = out
	}

	return out, nil
}

func (impl) Load(ctx context.Context, args args.Config) (GlobalConfig, error) {
	logger := log.FromContext(ctx)

	candidates := candidatePaths()
	if args.Conf != "" {
		candidates = []string{args.Conf}
	}

	for _, candidate := range candidates {
		_, err := os.Stat(candidate)
		if err == nil {
			logger.F("path", candidate).Debug("loading config")
			return loadFromPath(candidate)
		}
		logger.F("path", candidate, "error", err).Debug("config file not usable")
	}

	return nil, fmt.Errorf("no existing config file in: %s", strings.Join(candidates, ", "))
}

// EnvironmentForDest finds and returns an Environment matching the specified rsync
// destination, or nil if no Environment matches.
func (c *globalConfig) EnvironmentForDest(ctx context.Context, dest string) EnvironmentConfig {
	logger := log.FromContext(ctx)

	for i := range c.EnvironmentsRaw {
		out := &c.EnvironmentsRaw[i]
		if strings.HasPrefix(dest, out.Prefix()+":") {
			return out
		}
	}

	logger.F("dest", dest).Debug("no matching environment in config")

	return nil
}
