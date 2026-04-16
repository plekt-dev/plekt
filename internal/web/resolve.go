package web

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/plekt-dev/plekt/internal/loader"
)

// ExtensionResult is the resolved data from one extension.
type ExtensionResult struct {
	SourcePlugin string `json:"source_plugin"`
	Point        string `json:"point"`
	Type         string `json:"type"`
	Data         any    `json:"data"`
}

// ResolveExtensions calls each registered extension's data function and collects results.
// The page data (raw JSON) is passed as input so the extension can inspect item IDs etc.
func ResolveExtensions(ctx context.Context, pm loader.PluginManager, reg *loader.ExtensionRegistry, targetPlugin string, points []string, pageData []byte) []ExtensionResult {
	if reg == nil || len(points) == 0 {
		return nil
	}
	var results []ExtensionResult
	for _, point := range points {
		exts := reg.ForPoint(targetPlugin, point)
		for _, ext := range exts {
			callCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			out, err := pm.CallPlugin(callCtx, ext.SourcePlugin, ext.Descriptor.DataFunction, pageData)
			cancel()
			if err != nil {
				slog.Warn("extension call failed",
					"source", ext.SourcePlugin,
					"point", point,
					"function", ext.Descriptor.DataFunction,
					"error", err,
				)
				continue
			}
			var data any
			if err := json.Unmarshal(out, &data); err != nil {
				continue
			}
			results = append(results, ExtensionResult{
				SourcePlugin: ext.SourcePlugin,
				Point:        point,
				Type:         ext.Descriptor.Type,
				Data:         data,
			})
		}
	}
	return results
}

// ResolveAssetURLs checks whether the declared JS/CSS files exist on disk and,
// if so, returns the URL paths that serve them via PluginStaticHandler.
func ResolveAssetURLs(pluginsDir, pluginName string, assets *loader.FrontendAssets) (scriptURL, styleURL string) {
	if pluginsDir == "" || assets == nil {
		return "", ""
	}
	if filepath.Base(assets.JSFile) != assets.JSFile {
		scriptURL = ""
	} else if assets.JSFile != "" {
		p := filepath.Join(pluginsDir, pluginName, "frontend", assets.JSFile)
		if _, err := os.Stat(p); err == nil {
			scriptURL = "/p/" + pluginName + "/static/" + assets.JSFile
		}
	}
	if filepath.Base(assets.CSSFile) != assets.CSSFile {
		styleURL = ""
	} else if assets.CSSFile != "" {
		p := filepath.Join(pluginsDir, pluginName, "frontend", assets.CSSFile)
		if _, err := os.Stat(p); err == nil {
			styleURL = "/p/" + pluginName + "/static/" + assets.CSSFile
		}
	}
	return
}
