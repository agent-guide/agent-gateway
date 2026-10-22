package release_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	markdownLinkPattern = regexp.MustCompile(`!?\[[^]]*\]\(([^)[:space:]]+)`)
	htmlLinkPattern     = regexp.MustCompile(`href="([^"]+)"`)
)

func TestDocumentationLinks(t *testing.T) {
	root := repositoryRoot(t)
	paths := []string{
		filepath.Join(root, "README.md"),
		filepath.Join(root, "README.zh-CN.md"),
		filepath.Join(root, "CONTRIBUTING.md"),
		filepath.Join(root, "SECURITY.md"),
	}
	paths = append(paths, filesWithExtension(t, filepath.Join(root, "docs"), ".md")...)
	for _, path := range paths {
		t.Run(relativePath(t, root, path), func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, match := range markdownLinkPattern.FindAllSubmatch(raw, -1) {
				assertLocalLink(t, root, path, string(match[1]))
			}
		})
	}

	for _, path := range filesWithExtension(t, filepath.Join(root, "website"), ".html") {
		t.Run(relativePath(t, root, path), func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, match := range htmlLinkPattern.FindAllSubmatch(raw, -1) {
				assertLocalLink(t, root, path, string(match[1]))
			}
		})
	}
}

func TestBilingualReadmeParity(t *testing.T) {
	root := repositoryRoot(t)
	english := readFile(t, filepath.Join(root, "README.md"))
	chinese := readFile(t, filepath.Join(root, "README.zh-CN.md"))

	for _, token := range []string{"\n## ", "\n### ", "```"} {
		if strings.Count(english, token) != strings.Count(chinese, token) {
			t.Errorf("README token count differs for %q", token)
		}
	}

	englishLinks := markdownLinkTargets(english)
	chineseLinks := markdownLinkTargets(chinese)
	if len(englishLinks) != len(chineseLinks) {
		t.Fatalf("README link count differs: English=%d Chinese=%d", len(englishLinks), len(chineseLinks))
	}
	for index := range englishLinks {
		if englishLinks[index] != chineseLinks[index] {
			t.Errorf("README link %d differs: English=%q Chinese=%q", index, englishLinks[index], chineseLinks[index])
		}
	}
}

func TestBilingualWebsiteParity(t *testing.T) {
	root := repositoryRoot(t)
	english, err := filepath.Glob(filepath.Join(root, "website", "*.html"))
	if err != nil {
		t.Fatal(err)
	}
	if len(english) == 0 {
		t.Fatal("no English website pages found")
	}
	for _, enPath := range english {
		zhPath := filepath.Join(root, "website", "zh", filepath.Base(enPath))
		en := readFile(t, enPath)
		zh := readFile(t, zhPath)
		for _, token := range []string{"<section", "class=\"card\"", "<h2", "<h3"} {
			if strings.Count(en, token) != strings.Count(zh, token) {
				t.Errorf("%s count differs for %s and %s", token, relativePath(t, root, enPath), relativePath(t, root, zhPath))
			}
		}
		if !strings.Contains(en, `hreflang="zh"`) || !strings.Contains(zh, `hreflang="en"`) {
			t.Errorf("%s language alternates are incomplete", filepath.Base(enPath))
		}
		name := filepath.Base(enPath)
		enURL := "https://agentguide.online/" + name
		zhURL := "https://agentguide.online/zh/" + name
		if name == "index.html" {
			enURL = "https://agentguide.online/"
			zhURL = "https://agentguide.online/zh/"
		}
		for language, page := range map[string]string{"English": en, "Chinese": zh} {
			for _, token := range []string{
				`<meta property="og:title"`,
				`<meta property="og:description"`,
				`<meta name="twitter:card" content="summary">`,
				`<meta name="twitter:title"`,
				`<meta name="twitter:description"`,
			} {
				if strings.Count(page, token) != 1 {
					t.Errorf("%s %s page must contain one %q", name, language, token)
				}
			}
		}
		for language, pageURL := range map[string]string{"English": enURL, "Chinese": zhURL} {
			page := en
			if language == "Chinese" {
				page = zh
			}
			for _, token := range []string{
				`<link rel="canonical" href="` + pageURL + `">`,
				`<meta property="og:url" content="` + pageURL + `">`,
			} {
				if strings.Count(page, token) != 1 {
					t.Errorf("%s %s page must contain one %q", name, language, token)
				}
			}
		}
	}
}

func TestReleaseFacingFilesArePortable(t *testing.T) {
	root := repositoryRoot(t)
	command := exec.Command("git", "-C", root, "ls-files", "-z", "docs", "examples", "release", "website")
	output, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range strings.Split(string(output), "\x00") {
		if name == "" {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(name))
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "/Users/") || strings.Contains(string(raw), `C:\Users\`) {
			t.Errorf("%s contains a workstation-specific path", name)
		}
	}
}

func TestReleaseWorkflowGatesPublicationOnAnonymousImagePulls(t *testing.T) {
	root := repositoryRoot(t)
	workflow := readFile(t, filepath.Join(root, ".github", "workflows", "release.yml"))
	publicImagesStart := strings.Index(workflow, "\n  public-images:\n")
	imageSmokeStart := strings.Index(workflow, "\n  image-smoke:\n")
	publishStart := strings.Index(workflow, "\n  publish-release:\n")
	postPublishStart := strings.Index(workflow, "\n  post-publish:\n")
	if publicImagesStart < 0 || imageSmokeStart <= publicImagesStart ||
		publishStart < 0 || postPublishStart <= publishStart {
		t.Fatal("release workflow job boundaries are incomplete")
	}
	publicImages := workflow[publicImagesStart:imageSmokeStart]
	if strings.Contains(publicImages, "docker/login-action") {
		t.Fatal("public image gate authenticates to GHCR")
	}
	if strings.Count(publicImages, "check_public_image \"") != 2 {
		t.Fatal("public image gate does not pull both immutable images")
	}
	publishRelease := workflow[publishStart:postPublishStart]
	if !strings.Contains(publishRelease, "needs: [preflight, archives, images, public-images,") {
		t.Fatal("GitHub Release publication is not gated on anonymous image pulls")
	}
}

func TestArchiveReadmeUsesPortableChecksumCommands(t *testing.T) {
	root := repositoryRoot(t)
	readme := readFile(t, filepath.Join(root, "release", "archive-README.md"))
	for _, required := range []string{
		`archive="$(basename "$PWD").tar.gz"`,
		`grep " $archive\$" SHA256SUMS | sha256sum -c -`,
		`grep " $archive\$" SHA256SUMS | shasum -a 256 -c -`,
	} {
		if !strings.Contains(readme, required) {
			t.Errorf("archive README is missing portable checksum command %q", required)
		}
	}
	if strings.Contains(readme, "--ignore-missing") {
		t.Fatal("archive README requires a GNU-only checksum option")
	}
}

func assertLocalLink(t *testing.T, root, source, target string) {
	t.Helper()
	target = strings.Trim(target, "<>")
	if target == "" || strings.HasPrefix(target, "#") || strings.HasPrefix(target, "http://") ||
		strings.HasPrefix(target, "https://") || strings.HasPrefix(target, "mailto:") || strings.HasPrefix(target, "data:") {
		return
	}
	if filepath.IsAbs(target) {
		t.Errorf("repository-local link must be relative: %s", target)
		return
	}
	path := strings.SplitN(target, "#", 2)[0]
	path = strings.SplitN(path, "?", 2)[0]
	if path == "" {
		return
	}
	resolved := filepath.Clean(filepath.Join(filepath.Dir(source), filepath.FromSlash(path)))
	if rel, err := filepath.Rel(root, resolved); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		t.Errorf("local link escapes the repository: %s", target)
		return
	}
	if _, err := os.Stat(resolved); err != nil {
		t.Errorf("broken local link %q: %v", target, err)
	}
}

func filesWithExtension(t *testing.T, root, extension string) []string {
	t.Helper()
	var paths []string
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && filepath.Ext(path) == extension {
			paths = append(paths, path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return paths
}

func markdownLinkTargets(raw string) []string {
	matches := markdownLinkPattern.FindAllStringSubmatch(raw, -1)
	targets := make([]string, 0, len(matches))
	for _, match := range matches {
		targets = append(targets, match[1])
	}
	return targets
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func relativePath(t *testing.T, root, path string) string {
	t.Helper()
	rel, err := filepath.Rel(root, path)
	if err != nil {
		t.Fatal(fmt.Errorf("relative path: %w", err))
	}
	return rel
}
