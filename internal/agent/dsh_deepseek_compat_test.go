package agent

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/provider"
	"gopkg.in/yaml.v3"
)

// DSH's pi-ai route is named magpie and reaches loopback, so pi-ai cannot
// detect DeepSeek's thinking dialect from either its provider or its URL.
func TestDshDeepSeekThinkingCompat(t *testing.T) {
	efforts := []string{"none", "low", "high", "max"}
	models := []catalog.Model{
		{ID: "deepseek/deepseek-flash", Efforts: efforts},
		{ID: "deepseek/deepseek-v4-pro", Efforts: efforts},
		{ID: "relay/deepseek-ai/DeepSeek-V4.1-Flash", Efforts: efforts},
		{ID: "deepseek/gpt-5.5", Efforts: efforts},
		{ID: "group/deepseek-flash", Efforts: efforts},
		{ID: "relay/deepseek-r1", Efforts: []string{"low", "high"}},
		{ID: "relay/deepseek-chat"},
	}
	for _, api := range dshAPIs {
		t.Run(api, func(t *testing.T) {
			b, err := yaml.Marshal(dshRouteConfig(models, api, "http://127.0.0.1:3425"))
			if err != nil {
				t.Fatal(err)
			}
			var route struct {
				Models []struct {
					ID     string         `yaml:"id"`
					Compat map[string]any `yaml:"compat"`
				} `yaml:"models"`
			}
			if err := yaml.Unmarshal(b, &route); err != nil {
				t.Fatal(err)
			}
			for i, model := range route.Models {
				var want map[string]any
				if api == "openai-completions" && i < 3 {
					want = map[string]any{"thinkingFormat": "deepseek", "requiresReasoningContentOnAssistantMessages": true}
				}
				if !reflect.DeepEqual(model.Compat, want) {
					t.Errorf("%s compat = %v, want %v", model.ID, model.Compat, want)
				}
			}
		})
	}
}

func TestDshDeepSeekCompatOnConnectAndSync(t *testing.T) {
	home, dir, web := dshRouteHome(t)
	dshKeyFixture(dir)
	if err := provider.Save(provider.Provider{ID: "deepseek", Name: "DeepSeek", Chat: "https://api.deepseek.com/v1", Key: "k", Models: []string{"deepseek-flash", "deepseek-v4-pro"}}); err != nil {
		t.Fatal(err)
	}
	if err := catalog.SaveLive("deepseek", "https://api.deepseek.com/v1", []catalog.Model{
		{ID: "deepseek-flash", Efforts: []string{"none", "low", "high", "max"}},
		{ID: "deepseek-v4-pro", Efforts: []string{"none", "low", "high", "max"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(web, []byte(dshMineRow), 0o600); err != nil {
		t.Fatal(err)
	}
	a := dsh(home)
	if err := a.Field("model").Set("magpie/deepseek/deepseek-flash"); err != nil {
		t.Fatal(err)
	}
	for round := 0; round < 2; round++ {
		_, compat := dshRouteCompat(t, web, "magpie")
		for _, id := range []string{"deepseek/deepseek-flash", "deepseek/deepseek-v4-pro"} {
			if c := compat[id]; c["thinkingFormat"] != "deepseek" || c["requiresReasoningContentOnAssistantMessages"] != true {
				t.Fatalf("round %d, %s missing DeepSeek compat: %v", round, id, c)
			}
		}
		if err := a.Sync(); err != nil {
			t.Fatal(err)
		}
	}
	// A deliberate change in DSH's Models page is the user's, including false.
	b, err := os.ReadFile(web)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(b), "thinkingFormat: deepseek", "thinkingFormat: qwen", 1)
	edited = strings.Replace(edited, "requiresReasoningContentOnAssistantMessages: true", "requiresReasoningContentOnAssistantMessages: false", 1)
	if err := os.WriteFile(web, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	_, want := dshRouteCompat(t, web, "magpie")
	if err := a.Sync(); err != nil {
		t.Fatal(err)
	}
	_, got := dshRouteCompat(t, web, "magpie")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sync overwrote the user's compat: got %v, want %v", got, want)
	}
	if b, _ := os.ReadFile(web); !strings.Contains(string(b), "      mine:") {
		t.Fatal("sync removed the user's provider")
	}
}

// DSH may save a protocol change before magpie next reads the route. These
// Chat-only fields must not be copied onto Responses or Messages then.
func TestDshDeepSeekCompatLeavesOtherProtocols(t *testing.T) {
	for _, api := range []string{"openai-responses", "anthropic-messages"} {
		t.Run(api, func(t *testing.T) {
			var cur, next yaml.Node
			if err := yaml.Unmarshal([]byte("api: "+api+"\nmodels:\n  - id: deepseek/deepseek-flash\n    compat:\n      thinkingFormat: deepseek\n      requiresReasoningContentOnAssistantMessages: true\n"), &cur); err != nil {
				t.Fatal(err)
			}
			if err := yaml.Unmarshal([]byte("api: "+api+"\nmodels:\n  - id: deepseek/deepseek-flash\n"), &next); err != nil {
				t.Fatal(err)
			}
			dshKeepCompat(cur.Content[0], next.Content[0])
			out, err := yaml.Marshal(next.Content[0])
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(out), "thinkingFormat") || strings.Contains(string(out), "requiresReasoningContentOnAssistantMessages") {
				t.Fatalf("Chat-only compat stayed after switching to %s:\n%s", api, out)
			}
		})
	}
}

func TestDshDeepSeekCompatKeepsRouteDefaults(t *testing.T) {
	for _, perModel := range []bool{false, true} {
		t.Run(map[bool]string{false: "route only", true: "model override"}[perModel], func(t *testing.T) {
			raw := "- id: llm-pi-ai\n  config:\n    providers:\n      magpie:\n        api: openai-completions\n        compat:\n          thinkingFormat: qwen\n          requiresReasoningContentOnAssistantMessages: false\n        models:\n          - id: deepseek/deepseek-flash\n"
			if perModel {
				raw += "            compat:\n              thinkingFormat: deepseek\n"
			}
			head, items, err := dshParse("cordis.patch.yml", raw)
			if err != nil {
				t.Fatal(err)
			}
			models := []catalog.Model{
				{ID: "deepseek/deepseek-flash", Efforts: []string{"none", "low", "high", "max"}},
				{ID: "relay/gpt-5.5", Efforts: []string{"low", "medium", "high"}},
			}
			items, err = dshPutRoute(items, true, models, "http://127.0.0.1:3425")
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "cordis.patch.yml")
			if err := dshWrite(path, head, items); err != nil {
				t.Fatal(err)
			}
			_, compat := dshRouteCompat(t, path, "magpie")
			if c := compat["relay/gpt-5.5"]; c != nil {
				t.Fatalf("unrelated model acquired DeepSeek compat: %v", c)
			}
			var want map[string]any
			if perModel {
				want = map[string]any{"thinkingFormat": "deepseek"}
			}
			if got := compat["deepseek/deepseek-flash"]; !reflect.DeepEqual(got, want) {
				t.Fatalf("model defaults shadow the user's route compat: got %v, want %v", got, want)
			}
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(b), "thinkingFormat: qwen") || !strings.Contains(string(b), "requiresReasoningContentOnAssistantMessages: false") {
				t.Fatalf("route-level settings went:\n%s", b)
			}
		})
	}
}
