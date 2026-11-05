package infra

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"connectrpc.com/connect/v2"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/loco/cmdutil"
	domainv1 "github.com/team-loco/loco/gen/go/loco/domain/v1"
	"github.com/team-loco/loco/internal/locofile"
	"sigs.k8s.io/yaml"
)

const (
	domainSuffixLength = 4
	domainAlphabetSize = 26
)

var (
	errQuickDeployFlags    = errors.New("deployment flags for a new service require omitting loco.yaml")
	errNameConflict        = errors.New("set the service name with either an argument or --name")
	errMultipleNames       = errors.New("without loco.yaml, deploy one service at a time")
	errDomainConflict      = errors.New("--domain and --private cannot be used together")
	errUnsafeDirectoryName = errors.New("directory name has no usable letters or digits; set --name")
	errDomainUnavailable   = errors.New("generated public hostname is unavailable; set --domain")
)

func loadDeployFile(cmd *cobra.Command, filePath string) (*locofile.File, []byte, bool, error) {
	file, data, err := loadLocoFile(filePath)
	quickDeploy := errors.Is(err, os.ErrNotExist) && !cmd.Flags().Changed("file")
	if err != nil && !quickDeploy {
		return nil, nil, false, err
	}
	if !quickDeploy && changedQuickDeployFlag(cmd) {
		return nil, nil, false, errQuickDeployFlags
	}
	return file, data, quickDeploy, nil
}

func changedQuickDeployFlag(cmd *cobra.Command) bool {
	for _, name := range []string{"name", "port", "region", "domain", "private", "dockerfile", "context"} {
		if cmd.Flags().Changed(name) {
			return true
		}
	}
	return false
}

func buildQuickDeploy(cmd *cobra.Command, names []string, t cmdutil.Target) (*locofile.File, []byte, error) {
	name, err := quickServiceName(cmd, names)
	if err != nil {
		return nil, nil, err
	}
	config, err := fetchConfig(cmd)
	if err != nil {
		return nil, nil, err
	}
	defaults := config.GetServiceDefaults()
	port, err := cmd.Flags().GetInt32("port")
	if err != nil {
		return nil, nil, err
	}
	if !cmd.Flags().Changed("port") {
		port = defaults.GetRouting().GetPort()
	}
	region, err := cmd.Flags().GetString("region")
	if err != nil {
		return nil, nil, err
	}
	if region == "" {
		ctx := cmd.Context()
		region, err = fetchDefaultRegion(ctx, t)
		if err != nil {
			return nil, nil, err
		}
	}
	domain, err := quickDomain(cmd, t, name, region)
	if err != nil {
		return nil, nil, err
	}
	dockerfile, err := cmd.Flags().GetString("dockerfile")
	if err != nil {
		return nil, nil, err
	}
	contextDir, err := cmd.Flags().GetString("context")
	if err != nil {
		return nil, nil, err
	}
	image, err := cmd.Flags().GetString("image")
	if err != nil {
		return nil, nil, err
	}
	if cmd.Flags().Changed("image") && strings.TrimSpace(image) == "" {
		return nil, nil, errEmptyImage
	}
	minReplicas := defaults.GetMinReplicas()
	maxReplicas := defaults.GetMaxReplicas()
	service := locofile.Service{
		Port:       new(port),
		Dockerfile: dockerfile,
		Context:    contextDir,
		Image:      image,
		Regions: map[string]locofile.Region{region: {
			CPU:      defaults.GetCpu(),
			Memory:   defaults.GetMemory(),
			Replicas: &locofile.Replicas{Min: new(minReplicas), Max: new(maxReplicas)},
		}},
	}
	if domain != "" {
		service.Domains = []string{domain}
	}
	generated := locofile.File{
		Version:  locofile.Version,
		Partial:  name,
		Services: map[string]locofile.Service{name: service},
	}
	data, err := yaml.Marshal(generated)
	if err != nil {
		return nil, nil, fmt.Errorf("render deployment: %w", err)
	}
	file, err := locofile.Parse(data)
	if err != nil {
		return nil, nil, fmt.Errorf("generated deployment: %w", err)
	}
	return file, data, nil
}

func quickServiceName(cmd *cobra.Command, names []string) (string, error) {
	if len(names) > 1 {
		return "", errMultipleNames
	}
	name, err := cmd.Flags().GetString("name")
	if err != nil {
		return "", err
	}
	if name != "" && len(names) > 0 {
		return "", errNameConflict
	}
	if len(names) == 1 {
		name = names[0]
	}
	yes, err := cmd.Flags().GetBool("yes")
	if err != nil {
		return "", err
	}
	if name == "" && cmdutil.IsInteractive() && !yes {
		name, err = cmdutil.AskString(cmd, "Service name (press Enter to use the directory name): ")
		if err != nil {
			return "", fmt.Errorf("read service name: %w", err)
		}
	}
	if name == "" {
		name, err = directoryServiceName()
		if err != nil {
			return "", err
		}
	}
	return name, nil
}

func directoryServiceName() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get current directory: %w", err)
	}
	name := locofile.Slug(filepath.Base(dir))
	if name == "" {
		return "", errUnsafeDirectoryName
	}
	return name, nil
}

func quickDomain(cmd *cobra.Command, t cmdutil.Target, name, region string) (string, error) {
	domain, err := cmd.Flags().GetString("domain")
	if err != nil {
		return "", err
	}
	private, err := cmd.Flags().GetBool("private")
	if err != nil {
		return "", err
	}
	if private && domain != "" {
		return "", errDomainConflict
	}
	if !private && domain == "" {
		ctx := cmd.Context()
		platformDomain, domainErr := servedDomain(ctx, t, region)
		if domainErr != nil {
			return "", domainErr
		}
		return generatedDomain(ctx, t, name, platformDomain)
	}
	return domain, nil
}

func generatedDomain(ctx context.Context, t cmdutil.Target, name, platformDomain string) (string, error) {
	base := domainBase(name)
	existing, err := existingGeneratedDomain(ctx, t, name, base, platformDomain)
	if err != nil || existing != "" {
		return existing, err
	}
	client := t.Clients.Domain
	candidate := base + domainSuffix(t.WorkspaceID, name) + "." + platformDomain
	request := &domainv1.CheckDomainAvailabilityRequest{Domain: candidate}
	response, checkErr := client.CheckDomainAvailability(ctx, request)
	if checkErr != nil {
		return "", fmt.Errorf("check public hostname %s: %w", candidate, checkErr)
	}
	if !response.GetIsAvailable() {
		return "", fmt.Errorf("%w: %s", errDomainUnavailable, candidate)
	}
	return candidate, nil
}

func existingGeneratedDomain(ctx context.Context, t cmdutil.Target, name, base, platformDomain string) (string, error) {
	res, err := t.Service(ctx, name)
	if connect.CodeOf(err) == connect.CodeNotFound {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	for _, assigned := range res.GetDomains() {
		domain := assigned.GetDomain()
		if !strings.HasPrefix(domain, base) || !strings.HasSuffix(domain, "."+platformDomain) {
			continue
		}
		if isDomainSuffix(strings.TrimSuffix(strings.TrimPrefix(domain, base), "."+platformDomain)) {
			return domain, nil
		}
	}
	return "", nil
}

func domainBase(name string) string {
	prefix := locofile.Slug(name)
	if len(prefix) > locofile.MaxServiceNameLength-domainSuffixLength-1 {
		prefix = strings.TrimSuffix(prefix[:locofile.MaxServiceNameLength-domainSuffixLength-1], "-")
	}
	return prefix + "-"
}

func domainSuffix(workspaceID, name string) string {
	input := workspaceID + "/" + name
	hash := sha256.Sum256([]byte(input))
	value := binary.BigEndian.Uint32(hash[:])
	suffix := [domainSuffixLength]byte{}
	for index := domainSuffixLength - 1; index >= 0; index-- {
		suffix[index] = 'a' + byte(value%domainAlphabetSize)
		value /= domainAlphabetSize
	}
	return string(suffix[:])
}

func isDomainSuffix(suffix string) bool {
	if len(suffix) != domainSuffixLength {
		return false
	}
	for _, char := range suffix {
		if char < 'a' || char > 'z' {
			return false
		}
	}
	return true
}
