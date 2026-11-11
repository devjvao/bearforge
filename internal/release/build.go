package release

import (
	"bytes"
	"context"
	"crypto/sha256"
	binaryinfo "debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"bearforge/internal/domain"
	"bearforge/internal/gitcli"
	"bearforge/internal/process"
)

const ReleaseToolchain = "go1.27.4"

type Options struct {
	Source, Output, Version string
	Targets                 []string
	Snapshot                bool
}
type Target struct {
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
	Archive      string `json:"archive"`
	SBOM         string `json:"sbom"`
	BinarySHA256 string `json:"binary_sha256"`
}
type Manifest struct {
	Schema     int      `json:"schema_version"`
	Version    string   `json:"version"`
	Commit     string   `json:"commit"`
	CommitTime int64    `json:"commit_time"`
	Dirty      bool     `json:"dirty"`
	Snapshot   bool     `json:"snapshot"`
	GoVersion  string   `json:"go_version"`
	Targets    []Target `json:"targets"`
}

func buildEnvironment(source, goos, goarch string) []string {
	blocked := map[string]bool{"GOOS": true, "GOARCH": true, "CGO_ENABLED": true, "GOTOOLCHAIN": true, "GOFLAGS": true, "GOEXPERIMENT": true, "GOENV": true, "GOWORK": true, "GOAMD64": true, "GOARM64": true, "GO111MODULE": true, "GOROOT": true, "GODEBUG": true, "PWD": true}
	result := []string{}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !blocked[strings.ToUpper(key)] {
			result = append(result, entry)
		}
	}
	return append(result, "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0", "GOTOOLCHAIN=local", "GOFLAGS=", "GOEXPERIMENT=", "GOENV=off", "GOWORK=off", "GOAMD64=v1", "GOARM64=v8.0", "GO111MODULE=on", "PWD="+source)
}

func runBuildTool(ctx context.Context, root string, environment, argv []string) ([]byte, error) {
	r, err := (process.Runner{Environment: environment}).Run(ctx, domain.ProcessSpec{Directory: root, Argv: argv}, nil)
	if err != nil {
		return nil, err
	}
	if r.ExitCode != 0 {
		return nil, fmt.Errorf("%s failed (exit %d): %s", argv[0], r.ExitCode, r.Stderr)
	}
	return r.Stdout, nil
}

func Build(ctx context.Context, options Options) (Manifest, error) {
	manifest := Manifest{Schema: 1, Version: options.Version, Snapshot: options.Snapshot}
	if !ValidVersion(options.Version) {
		return manifest, fmt.Errorf("version must be a safe semantic version such as v0.2.0")
	}
	targets := slices.Clone(options.Targets)
	if len(targets) == 0 {
		targets = []string{"darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64", "windows/amd64", "windows/arm64"}
	}
	slices.Sort(targets)
	for i, target := range targets {
		goos, arch, ok := strings.Cut(target, "/")
		if !ok || (goos != "darwin" && goos != "linux" && goos != "windows") || (arch != "amd64" && arch != "arm64") || (i > 0 && targets[i-1] == target) {
			return manifest, fmt.Errorf("unsupported or duplicate target %q", target)
		}
	}
	root, err := filepath.Abs(options.Source)
	if err != nil {
		return manifest, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return manifest, err
	}
	output, err := filepath.Abs(options.Output)
	if err != nil {
		return manifest, err
	}
	entries, err := os.ReadDir(output)
	if err != nil && !os.IsNotExist(err) {
		return manifest, err
	}
	if len(entries) > 0 {
		return manifest, fmt.Errorf("output directory must be new or empty; existing artifacts are never overwritten")
	}
	git, err := gitcli.New()
	if err != nil {
		return manifest, err
	}
	commit, err := git.Run(ctx, root, "rev-parse", "HEAD")
	if err != nil {
		return manifest, err
	}
	manifest.Commit = strings.TrimSpace(string(commit))
	date, err := git.Run(ctx, root, "show", "-s", "--format=%ct", "HEAD")
	if err != nil {
		return manifest, err
	}
	manifest.CommitTime, err = strconv.ParseInt(strings.TrimSpace(string(date)), 10, 64)
	if err != nil {
		return manifest, err
	}
	status, err := git.Run(ctx, root, "status", "--porcelain", "--untracked-files=normal")
	if err != nil {
		return manifest, err
	}
	manifest.Dirty = len(status) > 0
	goPath, err := exec.LookPath("go")
	if err != nil {
		return manifest, err
	}
	environment := buildEnvironment(root, "", "")
	version, err := runBuildTool(ctx, root, environment, []string{goPath, "version"})
	if err != nil {
		return manifest, err
	}
	fields := strings.Fields(string(version))
	if len(fields) < 3 {
		return manifest, fmt.Errorf("unable to identify Go toolchain")
	}
	manifest.GoVersion = fields[2]
	if !options.Snapshot {
		if manifest.GoVersion != ReleaseToolchain {
			return manifest, fmt.Errorf("production releases require %s; use --snapshot for local validation", ReleaseToolchain)
		}
		if manifest.Dirty {
			return manifest, fmt.Errorf("production releases require a clean source tree")
		}
		if err := verifyVersionTag(ctx, git, root, options.Version, manifest.Commit); err != nil {
			return manifest, err
		}
	}
	if _, err := runBuildTool(ctx, root, environment, []string{goPath, "mod", "verify"}); err != nil {
		return manifest, err
	}
	documents := []File{}
	for _, name := range []string{"LICENSE", "README.md", "docs/onboarding.md", "docs/releases.md"} {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			return manifest, err
		}
		documents = append(documents, File{Name: name, Data: data, Mode: 0644})
	}
	if err := os.MkdirAll(output, 0755); err != nil {
		return manifest, err
	}
	artifacts := map[string][]byte{}
	for _, platform := range targets {
		if err := ctx.Err(); err != nil {
			return manifest, err
		}
		goos, arch, _ := strings.Cut(platform, "/")
		binary, sbom, err := buildBinary(ctx, root, output, goPath, goos, arch, manifest)
		if err != nil {
			return manifest, err
		}
		prefix := "bearforge_" + strings.TrimPrefix(options.Version, "v") + "_" + goos + "_" + arch
		format, binaryName := "tar.gz", "bearforge"
		if goos == "windows" {
			format, binaryName = "zip", "bearforge.exe"
		}
		files := slices.Clone(documents)
		files = append(files, File{Name: binaryName, Data: binary, Mode: 0755}, File{Name: "sbom.cdx.json", Data: sbom, Mode: 0644})
		var archive bytes.Buffer
		if err := Archive(&archive, format, files, time.Unix(manifest.CommitTime, 0)); err != nil {
			return manifest, err
		}
		archiveName, sbomName := prefix+"."+format, prefix+".sbom.cdx.json"
		artifacts[archiveName], artifacts[sbomName] = archive.Bytes(), sbom
		sum := sha256.Sum256(binary)
		manifest.Targets = append(manifest.Targets, Target{OS: goos, Architecture: arch, Archive: archiveName, SBOM: sbomName, BinarySHA256: hex.EncodeToString(sum[:])})
	}
	if !options.Snapshot {
		current, err := git.Run(ctx, root, "rev-parse", "HEAD")
		if err != nil {
			return manifest, err
		}
		status, err := git.Run(ctx, root, "status", "--porcelain", "--untracked-files=normal")
		if err != nil {
			return manifest, err
		}
		if len(status) > 0 || strings.TrimSpace(string(current)) != manifest.Commit {
			return manifest, fmt.Errorf("source changed during the production build")
		}
		if err := verifyVersionTag(ctx, git, root, options.Version, manifest.Commit); err != nil {
			return manifest, err
		}
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return manifest, err
	}
	artifacts["manifest.json"] = append(data, '\n')
	checksums, err := Checksums(artifacts)
	if err != nil {
		return manifest, err
	}
	for name, data := range artifacts {
		if err := os.WriteFile(filepath.Join(output, name), data, 0644); err != nil {
			return manifest, err
		}
	}
	if err := os.WriteFile(filepath.Join(output, "checksums.txt"), checksums, 0644); err != nil {
		return manifest, err
	}
	return manifest, nil
}

func verifyVersionTag(ctx context.Context, git *gitcli.Client, root, version, commit string) error {
	tag, err := git.Run(ctx, root, "rev-parse", "--verify", "refs/tags/"+version+"^{commit}")
	if err != nil || strings.TrimSpace(string(tag)) != commit {
		return fmt.Errorf("production version must match the requested tag at HEAD")
	}
	return nil
}

func buildBinary(ctx context.Context, root, output, goPath, goos, arch string, manifest Manifest) ([]byte, []byte, error) {
	temporary, err := os.CreateTemp(output, ".bearforge-build-*")
	if err != nil {
		return nil, nil, err
	}
	name := temporary.Name()
	if err := temporary.Close(); err != nil {
		return nil, nil, err
	}
	defer func() { _ = os.Remove(name) }()
	flags := "-s -w -X bearforge/internal/buildinfo.Version=" + manifest.Version + " -X bearforge/internal/buildinfo.Commit=" + manifest.Commit + " -X bearforge/internal/buildinfo.Dirty=" + strconv.FormatBool(manifest.Dirty)
	argv := []string{goPath, "build", "-mod=readonly", "-trimpath", "-buildvcs=false", "-ldflags=" + flags, "-o", name, "./cmd/bearforge"}
	if _, err := runBuildTool(ctx, root, buildEnvironment(root, goos, arch), argv); err != nil {
		return nil, nil, err
	}
	binary, err := os.ReadFile(name)
	if err != nil {
		return nil, nil, err
	}
	info, err := binaryinfo.ReadFile(name)
	if err != nil {
		return nil, nil, err
	}
	type component struct {
		Type    string `json:"type"`
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	components := []component{{Type: "library", Name: "go-stdlib", Version: info.GoVersion}}
	for _, module := range info.Deps {
		if module.Replace != nil {
			module = module.Replace
		}
		if module.Version == "" && !manifest.Snapshot {
			return nil, nil, fmt.Errorf("production dependencies must not use local replacements")
		}
		components = append(components, component{Type: "library", Name: module.Path, Version: module.Version})
	}
	slices.SortFunc(components, func(a, b component) int { return strings.Compare(a.Name, b.Name) })
	sbom := struct {
		Format   string `json:"bomFormat"`
		Spec     string `json:"specVersion"`
		Version  int    `json:"version"`
		Metadata struct {
			Component component `json:"component"`
		} `json:"metadata"`
		Components []component `json:"components"`
	}{Format: "CycloneDX", Spec: "1.6", Version: 1, Components: components}
	sbom.Metadata.Component = component{Type: "application", Name: "bearforge", Version: manifest.Version}
	data, err := json.MarshalIndent(sbom, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	return binary, append(data, '\n'), nil
}
