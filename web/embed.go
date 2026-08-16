package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// dist 在发布构建前由 `yarn build` 生成。all: 前缀保留构建产物中的点文件。
//
//go:embed all:dist
var assets embed.FS

// Handler 将支付 API 交给 api，并将所有非 API 请求作为单页应用路由返回。
func Handler(api http.Handler) http.Handler {
	dist, err := fs.Sub(assets, "dist")
	if err != nil {
		panic("embedded web/dist is unavailable; run yarn build before compiling the Go service")
	}
	files := http.FileServer(http.FS(dist))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isAPIPath(r.URL.Path) {
			api.ServeHTTP(w, r)
			return
		}

		requested := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if requested != "" && requested != "." {
			if file, openErr := dist.Open(requested); openErr == nil {
				_ = file.Close()
				if strings.HasPrefix(requested, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
		}

		// React Router / 深链接回退到入口页，避免直接刷新页面时出现 404。
		fallback := r.Clone(r.Context())
		fallback.URL.Path = "/"
		fallback.URL.RawPath = ""
		w.Header().Set("Cache-Control", "no-cache")
		files.ServeHTTP(w, fallback)
	})
}

func isAPIPath(path string) bool {
	return path == "/healthz" ||
		path == "/submit.php" ||
		path == "/mapi.php" ||
		path == "/api.php" ||
		strings.HasPrefix(path, "/api/") ||
		strings.HasPrefix(path, "/.well-known/")
}
