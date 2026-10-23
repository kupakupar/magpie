package davsync

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/yetone/magpie/internal/backup"
	"github.com/yetone/magpie/internal/library"
	"github.com/yetone/magpie/internal/profile"
)

// Rejoining a previously excluded library must not silently publish its old
// local contents over a server version the computer has never brought in.
func TestLibraryResumeDoesNotPublishExcludedLocal(t *testing.T) {
	for _, conditional := range []bool{false, true} {
		for _, legacy := range []bool{false, true} {
			for _, restore := range []bool{false, true} {
				name := "after-sync-with-library-off"
				if restore {
					name = "after-restore-with-library-off"
				}
				if conditional {
					name += "/304"
				} else {
					name += "/200"
				}
				if legacy {
					name += "/legacy-baseline"
				}
				t.Run(name, func(t *testing.T) {
					fake := &fakeDAV{files: map[string][]byte{}, etags: map[string]string{}, dirs: map[string]bool{"/dav": true}, cond: conditional}
					server := httptest.NewServer(fake)
					defer server.Close()
					ctx := context.Background()
					cfg := Config{URL: server.URL + "/dav/", User: "me", Password: "pw", Passphrase: "fixture passphrase", Keys: true}
					say := func(text string) {
						t.Helper()
						if _, err := library.SaveInstructions(library.InstructionsChange{Shared: &text}); err != nil {
							t.Fatal(err)
						}
					}
					a, b := newComputer(t), newComputer(t)
					a.use(t)
					say("Latest rules from computer A.")
					if err := Configure(cfg); err != nil {
						t.Fatal(err)
					}
					if err := Now(ctx); err != nil {
						t.Fatal(err)
					}

					b.use(t)
					say("Old rules kept on computer B.")
					off := false
					cfg.Library = &off
					if err := Configure(cfg); err != nil {
						t.Fatal(err)
					}
					if restore {
						if _, err := Restore(ctx); err != nil {
							t.Fatal(err)
						}
					} else if err := Now(ctx); err != nil {
						t.Fatal(err)
					}
					view, err := library.ReadInstructions()
					if err != nil || view.Shared != "Old rules kept on computer B." {
						t.Fatalf("excluded library changed: %q, %v", view.Shared, err)
					}
					st := loadState()
					if !legacy && st.Local["library"] != "" {
						t.Fatal("excluded library acquired a baseline")
					}
					if legacy {
						local, err := collect(cfg)
						if err != nil {
							t.Fatal(err)
						}
						st.Local["library"] = hashes(local)["library"]
						saveState(st)
					}

					on := true
					cfg.Library = &on
					if err := Configure(cfg); err != nil {
						t.Fatal(err)
					}
					if err := Now(ctx); err != nil {
						t.Fatal(err)
					}
					fake.mu.Lock()
					raw := append([]byte(nil), fake.files["/dav/magpie/magpie.magpie-backup"]...)
					fake.mu.Unlock()
					remote, err := backup.Open(raw, cfg.Passphrase)
					if err != nil {
						t.Fatal(err)
					}
					if got := remote.Library.Texts["default"]; got != "Latest rules from computer A." {
						t.Fatalf("enabling library sync replaced the server with previously excluded local content: %q", got)
					}
					view, err = library.ReadInstructions()
					if err != nil || view.Shared != "Latest rules from computer A." {
						t.Fatalf("did not receive server library: %q, %v", view.Shared, err)
					}
					notice := Status().Notice
					if notice == nil || !slices.Contains(notice.Here, "library") {
						t.Fatalf("no recoverable replacement notice: %+v", notice)
					}
					files, err := filepath.Glob(filepath.Join(notice.Saved, "*-this-computer"+backup.Ext))
					if err != nil || len(files) != 1 {
						t.Fatalf("missing local backup: %v, %v", files, err)
					}
					kept, err := backup.Open(must(os.ReadFile(files[0])), cfg.Passphrase)
					if err != nil || kept.Library.Texts["default"] != "Old rules kept on computer B." {
						t.Fatalf("old library not backed up: %v", err)
					}
					puts := fake.puts
					if err := Now(ctx); err != nil {
						t.Fatal(err)
					}
					if fake.puts != puts {
						t.Fatal("unchanged joined library uploaded again")
					}
					say("An intentional edit after joining.")
					if err := Now(ctx); err != nil {
						t.Fatal(err)
					}
					remote, err = backup.Open(fake.files["/dav/magpie/magpie.magpie-backup"], cfg.Passphrase)
					if err != nil || remote.Library.Texts["default"] != "An intentional edit after joining." {
						t.Fatalf("later edit not uploaded: %v", err)
					}
				})
			}
		}
	}
}

func TestAgentsResumeDoesNotPublishExcludedLocal(t *testing.T) {
	fake := &fakeDAV{files: map[string][]byte{}, etags: map[string]string{}, dirs: map[string]bool{"/dav": true}, cond: true}
	server := httptest.NewServer(fake)
	defer server.Close()
	cfg := Config{URL: server.URL + "/dav/", User: "me", Password: "pw", Passphrase: "fixture passphrase", Agents: true}
	ctx := context.Background()
	a, b := newComputer(t), newComputer(t)
	set := func(c computer, model string) {
		t.Helper()
		c.use(t)
		t.Setenv("USERPROFILE", string(c))
		p := filepath.Join(string(c), ".claude", "settings.json")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(`{"model":"`+model+`"}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	set(a, "new-server-model")
	if err := Configure(cfg); err != nil {
		t.Fatal(err)
	}
	if err := Now(ctx); err != nil {
		t.Fatal(err)
	}
	set(b, "old-local-model")
	cfg.Agents = false
	if err := Configure(cfg); err != nil {
		t.Fatal(err)
	}
	if err := Now(ctx); err != nil {
		t.Fatal(err)
	}
	// Migrate an old writer's empty agents baseline too.
	st := loadState()
	st.Local["agents"] = hashes(backup.Bundle{})["agents"]
	saveState(st)
	cfg.Agents = true
	if err := Configure(cfg); err != nil {
		t.Fatal(err)
	}
	if err := Now(ctx); err != nil {
		t.Fatal(err)
	}
	if got := profile.Fields()["claude.model"]; got != "new-server-model" {
		t.Fatalf("excluded model replaced server choice: %q", got)
	}
	remote, err := backup.Open(fake.files["/dav/magpie/magpie.magpie-backup"], cfg.Passphrase)
	if err != nil || remote.Agents["claude.model"] != "new-server-model" {
		t.Fatalf("server model overwritten: %v", err)
	}
}
