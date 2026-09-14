package scripts

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var frontendDirs = []string{"internal/ui/web/ui", "internal/tokentracer/dashboard"}

type webFixture struct {
	t    *testing.T
	root string
}

func newWebFixture(t *testing.T) webFixture {
	t.Helper()
	f := webFixture{t: t, root: filepath.Join(t.TempDir(), "workspace with spaces")}
	for _, name := range []string{"check-web-dist.sh", "../Makefile"} {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		target := "scripts/check-web-dist.sh"
		if name == "../Makefile" {
			target = "Makefile"
		}
		f.write(target, string(data))
	}
	for _, dir := range frontendDirs {
		for _, name := range []string{"package.json", "package-lock.json", "tsconfig.json", "tsconfig.app.json", "tsconfig.node.json"} {
			f.write(dir+"/"+name, "{}\n")
		}
		f.write(dir+"/vite.config.ts", "export default {}\n")
		f.write(dir+"/index.html", "<div id='root'></div>\n")
		f.write(dir+"/src/main.ts", "export const value = 1\n")
		f.write(dir+"/public/asset with spaces.txt", "public asset\n")
		f.write(dir+"/.env.production", "VITE_EXAMPLE=fixture\n")
		f.write(dir+"/dist/index.html", "<script src='/assets/app.js'></script>\n")
		f.write(dir+"/dist/assets/app.js", "console.log(1)\n")
	}
	return f
}

func (f webFixture) write(name, content string) {
	f.t.Helper()
	path := filepath.Join(f.root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		f.t.Fatal(err)
	}
}

func (f webFixture) run(env []string, name string, args ...string) (string, error) {
	f.t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = f.root
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "PAW_SKIP_WEB_DIST_CHECK=") && !strings.HasPrefix(entry, "MAKEFLAGS=") && !strings.HasPrefix(entry, "MFLAGS=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, env...)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func (f webFixture) stamp() {
	f.t.Helper()
	for _, dir := range frontendDirs {
		if output, err := f.run(nil, "bash", "scripts/check-web-dist.sh", "--write", dir); err != nil {
			f.t.Fatalf("stamp: %v\n%s", err, output)
		}
	}
}

func TestWebDistFreshness(t *testing.T) {
	for _, dir := range frontendDirs {
		t.Run(dir, func(t *testing.T) {
			for _, name := range []string{
				"src/main.ts", "src/new file.ts", "public/asset with spaces.txt",
				"index.html", "package.json", "package-lock.json", "tsconfig.json",
				"tsconfig.app.json", "tsconfig.node.json", "vite.config.ts", ".env.production",
				"dist/assets/app.js", "dist/index.html",
			} {
				t.Run(name, func(t *testing.T) {
					f := newWebFixture(t)
					f.stamp()
					f.write(dir+"/"+name, "changed\n")
					output, err := f.run(nil, "bash", "scripts/check-web-dist.sh")
					if err == nil || !strings.Contains(output, "npm run build") {
						t.Fatalf("changed %s must fail with rebuild guidance: %v\n%s", name, err, output)
					}
				})
			}
		})
	}
}

func TestWebDistIgnoresMtimeAndLocalCaches(t *testing.T) {
	f := newWebFixture(t)
	f.stamp()
	for _, dir := range frontendDirs {
		if err := os.Chtimes(filepath.Join(f.root, dir, "src/main.ts"), time.Now().Add(time.Hour), time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		f.write(dir+"/node_modules/cache", "local only")
		f.write(dir+"/tsconfig.app.tsbuildinfo", "local only")
	}
	if output, err := f.run(nil, "bash", "scripts/check-web-dist.sh"); err != nil {
		t.Fatalf("unchanged contents: %v\n%s", err, output)
	}
}

func TestWebDistFingerprintSurvivesCheckoutMoveAndSourceRevert(t *testing.T) {
	original, moved := newWebFixture(t), newWebFixture(t)
	original.stamp()
	for _, dir := range frontendDirs {
		stamp, err := os.ReadFile(filepath.Join(original.root, dir, "dist/.paw-source-sha256"))
		if err != nil {
			t.Fatal(err)
		}
		moved.write(dir+"/dist/.paw-source-sha256", string(stamp))
		moved.write(dir+"/src/main.ts", "temporary edit\n")
		moved.write(dir+"/src/main.ts", "export const value = 1\n")
	}
	if output, err := moved.run(nil, "bash", "scripts/check-web-dist.sh"); err != nil {
		t.Fatalf("same contents at different checkout path must pass: %v\n%s", err, output)
	}
}

func TestWebDistMissingFiles(t *testing.T) {
	for _, name := range []string{"src/main.ts", "package-lock.json", "dist/index.html", "dist/assets/app.js", "dist/.paw-source-sha256"} {
		t.Run(name, func(t *testing.T) {
			f := newWebFixture(t)
			f.stamp()
			if err := os.Remove(filepath.Join(f.root, frontendDirs[0], name)); err != nil {
				t.Fatal(err)
			}
			if output, err := f.run(nil, "bash", "scripts/check-web-dist.sh"); err == nil {
				t.Fatalf("missing %s accepted: %s", name, output)
			}
		})
	}
}

func TestWebDistStampRejectsIncompleteBuild(t *testing.T) {
	for _, name := range []string{"package-lock.json", "dist/index.html"} {
		t.Run(name, func(t *testing.T) {
			f := newWebFixture(t)
			if err := os.Remove(filepath.Join(f.root, frontendDirs[0], name)); err != nil {
				t.Fatal(err)
			}
			if output, err := f.run(nil, "bash", "scripts/check-web-dist.sh", "--write", frontendDirs[0]); err == nil {
				t.Fatalf("stamped incomplete input/output: %s", output)
			}
			if _, err := os.Stat(filepath.Join(f.root, frontendDirs[0], "dist/.paw-source-sha256")); !os.IsNotExist(err) {
				t.Fatalf("failed build must not create stamp: %v", err)
			}
		})
	}
}

func TestWebDistStampRejectsHashFailure(t *testing.T) {
	f := newWebFixture(t)
	for _, name := range []string{"sha256sum", "shasum"} {
		f.write("tools/"+name, "#!/usr/bin/env bash\nexit 42\n")
	}
	env := []string{"PATH=" + filepath.Join(f.root, "tools") + string(os.PathListSeparator) + os.Getenv("PATH")}
	if output, err := f.run(env, "bash", "scripts/check-web-dist.sh", "--write", frontendDirs[0]); err == nil {
		t.Fatalf("hash failure stamped as success: %s", output)
	}
}

func TestWebDistCheckerChangeInvalidatesStamp(t *testing.T) {
	f := newWebFixture(t)
	f.stamp()
	path := filepath.Join(f.root, "scripts/check-web-dist.sh")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	f.write("scripts/check-web-dist.sh", string(data)+"\n# changed fingerprint recipe\n")
	if output, err := f.run(nil, "bash", "scripts/check-web-dist.sh"); err == nil {
		t.Fatalf("changed fingerprint recipe accepted: %s", output)
	}
}

func TestMakeBuildWebGate(t *testing.T) {
	for _, stale := range []bool{false, true} {
		name := "fresh"
		if stale {
			name = "stale"
		}
		t.Run(name, func(t *testing.T) {
			f := newWebFixture(t)
			f.stamp()
			if stale {
				f.write(frontendDirs[0]+"/src/main.ts", "changed\n")
			}
			f.write("tools/go", "#!/usr/bin/env bash\nprintf '%s\\n' \"$@\" > \"$WEB_TEST_GO_LOG\"\n")
			f.write("tools/npm", "#!/usr/bin/env bash\nprintf invoked > \"$WEB_TEST_NPM_LOG\"\nexit 99\n")
			goLog, npmLog := filepath.Join(f.root, "go.log"), filepath.Join(f.root, "npm.log")
			binDir := filepath.Join(f.root, "installed bin")
			f.write("installed bin/paw", "existing binary")
			env := []string{"PATH=" + filepath.Join(f.root, "tools") + string(os.PathListSeparator) + os.Getenv("PATH"), "WEB_TEST_GO_LOG=" + goLog, "WEB_TEST_NPM_LOG=" + npmLog}
			output, err := f.run(env, "make", "build", "BINDIR="+binDir)
			if stale {
				if err == nil {
					t.Fatalf("make accepted stale assets: %s", output)
				}
				if _, err := os.Stat(goLog); !os.IsNotExist(err) {
					t.Fatalf("Go invoked despite stale assets: %v", err)
				}
			} else {
				if err != nil {
					t.Fatalf("fresh make build: %v\n%s", err, output)
				}
				args, err := os.ReadFile(goLog)
				if err != nil || !strings.Contains(string(args), binDir+"/paw\n") {
					t.Fatalf("BINDIR not passed intact: %v\n%s", err, args)
				}
			}
			if _, err := os.Stat(npmLog); !os.IsNotExist(err) {
				t.Fatalf("make build must never invoke npm: %v", err)
			}
			data, err := os.ReadFile(filepath.Join(binDir, "paw"))
			if err != nil || string(data) != "existing binary" {
				t.Fatalf("gate damaged installed binary: %v", err)
			}
		})
	}
}

func TestFrontendBuildWritesStampAfterSuccess(t *testing.T) {
	for _, dir := range frontendDirs {
		t.Run(dir, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("..", dir, "package.json"))
			if err != nil {
				t.Fatal(err)
			}
			var pkg struct {
				Scripts map[string]string `json:"scripts"`
			}
			if err := json.Unmarshal(data, &pkg); err != nil {
				t.Fatal(err)
			}
			f := newWebFixture(t)
			for _, fail := range []bool{true, false} {
				vite := "#!/usr/bin/env bash\nexit 0\n"
				if fail {
					vite = "#!/usr/bin/env bash\nexit 1\n"
				}
				f.write("tools/vite", vite)
				f.write("tools/npm", "#!/usr/bin/env bash\nexit 0\n")
				env := []string{"PATH=" + filepath.Join(f.root, "tools") + string(os.PathListSeparator) + os.Getenv("PATH")}
				cmd := exec.Command("bash", "-c", pkg.Scripts["build"])
				cmd.Dir = filepath.Join(f.root, dir)
				cmd.Env = append(os.Environ(), env...)
				output, err := cmd.CombinedOutput()
				if (err != nil) != fail {
					t.Fatalf("build failure=%v: %v\n%s", fail, err, output)
				}
				_, err = os.Stat(filepath.Join(f.root, dir, "dist/.paw-source-sha256"))
				if fail && !os.IsNotExist(err) || !fail && err != nil {
					t.Fatalf("stamp must only exist after successful Vite build: %v", err)
				}
			}
		})
	}
}

func TestPrePushWebGate(t *testing.T) {
	hook, err := os.ReadFile("../.githooks/pre-push")
	if err != nil {
		t.Fatal(err)
	}
	copy, err := os.ReadFile("pre-push.sh")
	if err != nil || !bytes.Equal(hook, copy) {
		t.Fatalf("pre-push copies differ: %v", err)
	}
	f := newWebFixture(t)
	f.stamp()
	f.write(frontendDirs[1]+"/src/main.ts", "changed\n")
	f.write(".githooks/pre-push", string(hook))
	f.write("tools/git", "#!/usr/bin/env bash\nprintf '0123456789\\n'\n")
	f.write("tools/go", "#!/usr/bin/env bash\nprintf invoked > \"$WEB_TEST_GO_LOG\"\nexit 99\n")
	f.write("installed bin/paw", "existing binary")
	goLog := filepath.Join(f.root, "go.log")
	env := []string{"PATH=" + filepath.Join(f.root, "tools") + string(os.PathListSeparator) + os.Getenv("PATH"), "GOBIN=" + filepath.Join(f.root, "installed bin"), "WEB_TEST_GO_LOG=" + goLog}
	output, err := f.run(env, "bash", "-c", "printf '%s\\n' 'refs/heads/dev abc refs/heads/dev def' | ./.githooks/pre-push")
	if err == nil || !strings.Contains(output, "npm run build") {
		t.Fatalf("pre-push must reject stale assets: %v\n%s", err, output)
	}
	if _, err := os.Stat(goLog); !os.IsNotExist(err) {
		t.Fatalf("pre-push invoked Go before passing gate: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(f.root, "installed bin/paw"))
	if err != nil || string(data) != "existing binary" {
		t.Fatalf("pre-push damaged installed binary: %v", err)
	}
}
