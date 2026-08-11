package adminui

import (
	"strings"
	"testing"
)

func TestAdminScriptSupportsBundledAndroidWebView(t *testing.T) {
	script, err := assets.ReadFile("assets/app.js")
	if err != nil {
		t.Fatal(err)
	}

	source := string(script)
	unsupported := map[string]string{
		"optional chaining":  "?.",
		"nullish coalescing": "??",
	}
	for name, syntax := range unsupported {
		if strings.Contains(source, syntax) {
			t.Errorf("app.js contains unsupported %s syntax %q", name, syntax)
		}
	}

	requiredPolyfills := []string{
		"_objectSpread",
		"Array.prototype.flatMap",
		"Object.fromEntries",
		"HTMLFormElement.prototype.requestSubmit",
	}
	for _, polyfill := range requiredPolyfills {
		if !strings.Contains(source, polyfill) {
			t.Errorf("app.js is missing the Android WebView compatibility polyfill %q", polyfill)
		}
	}
}
