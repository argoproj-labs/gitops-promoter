/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package webserver

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing/fstest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/gin-gonic/gin"
)

func newPluginsRouter(pluginsDir string) *gin.Engine {
	return newPluginsRouterWithDistFS(pluginsDir, nil)
}

func newPluginsRouterWithDistFS(pluginsDir string, distFS fstest.MapFS) *gin.Engine {
	gin.SetMode(gin.TestMode)
	ws := &WebServer{PluginsDir: pluginsDir}
	if distFS != nil {
		ws.distFS = distFS
	}
	bundle, etag, err := ws.buildPluginsBundle()
	Expect(err).NotTo(HaveOccurred())
	ws.pluginsBundle = bundle
	ws.pluginsETag = etag

	router := gin.New()
	router.GET("/plugins.js", ws.httpPlugins)
	return router
}

var _ = Describe("httpPlugins", func() {
	It("concatenates matching .js files wrapped in try/catch with correct headers", func() {
		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, "plugin-a.js"), []byte("console.log('a')"), 0o644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, "plugin-b.js"), []byte("console.log('b')"), 0o644)).To(Succeed())

		router := newPluginsRouter(dir)
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/plugins.js", nil)
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusOK))
		Expect(w.Header().Get("Content-Type")).To(ContainSubstring("javascript"))
		Expect(w.Header().Get("Cache-Control")).To(Equal("no-cache"))
		Expect(w.Header().Get("ETag")).NotTo(BeEmpty())

		body := w.Body.String()
		Expect(body).To(ContainSubstring("try {\nconsole.log('a')\n} catch(e) { console.error('Plugin plugin-a.js failed to load:', e); }"))
		Expect(body).To(ContainSubstring("try {\nconsole.log('b')\n} catch(e) { console.error('Plugin plugin-b.js failed to load:', e); }"))
	})

	It("returns an empty 200 when the plugins directory does not exist", func() {
		router := newPluginsRouter(filepath.Join(GinkgoT().TempDir(), "does-not-exist"))
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/plugins.js", nil)
		router.ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusOK))
		Expect(w.Body.String()).To(BeEmpty())
		Expect(w.Header().Get("ETag")).NotTo(BeEmpty())
	})

	It("returns 304 when If-None-Match matches the current ETag", func() {
		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, "plugin-a.js"), []byte("console.log('a')"), 0o644)).To(Succeed())

		router := newPluginsRouter(dir)

		w1 := httptest.NewRecorder()
		router.ServeHTTP(w1, httptest.NewRequest(http.MethodGet, "/plugins.js", nil))
		etag := w1.Header().Get("ETag")
		Expect(etag).NotTo(BeEmpty())

		w2 := httptest.NewRecorder()
		req2 := httptest.NewRequest(http.MethodGet, "/plugins.js", nil)
		req2.Header.Set("If-None-Match", etag)
		router.ServeHTTP(w2, req2)

		Expect(w2.Code).To(Equal(http.StatusNotModified))
		Expect(w2.Body.String()).To(BeEmpty())
	})

	It("skips symlinked .js files", func() {
		dir := GinkgoT().TempDir()
		realFile := filepath.Join(dir, "real.js")
		Expect(os.WriteFile(realFile, []byte("console.log('real')"), 0o644)).To(Succeed())
		Expect(os.Symlink(realFile, filepath.Join(dir, "plugin-link.js"))).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, "plugin-real.js"), []byte("console.log('included')"), 0o644)).To(Succeed())

		router := newPluginsRouter(dir)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/plugins.js", nil))

		Expect(w.Code).To(Equal(http.StatusOK))
		Expect(w.Body.String()).To(ContainSubstring("included"))
		Expect(w.Body.String()).NotTo(ContainSubstring("plugin-link.js"))
	})

	It("does not include non-.js files", func() {
		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, "plugin-a.js"), []byte("console.log('a')"), 0o644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, "plugin-a.txt"), []byte("not js"), 0o644)).To(Succeed())

		router := newPluginsRouter(dir)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/plugins.js", nil))

		Expect(w.Code).To(Equal(http.StatusOK))
		Expect(w.Body.String()).NotTo(ContainSubstring("not js"))
	})

	It("concatenates a malformed plugin file as-is, same as ArgoCD's /extensions.js", func() {
		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, "plugin-good.js"), []byte("console.log('good')"), 0o644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dir, "plugin-broken.js"), []byte("console.log('broken'"), 0o644)).To(Succeed())

		router := newPluginsRouter(dir)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/plugins.js", nil))

		// There is no JS parser available to pre-validate syntax (see
		// concat-plugins.mjs's new Function() check, which has no Go
		// equivalent here), so - matching ArgoCD's own serveExtensions
		// handler - a malformed file is concatenated as-is rather than
		// skipped. The per-file try/catch wrapper only guards runtime
		// errors; a syntax error still fails to parse the whole response.
		Expect(w.Code).To(Equal(http.StatusOK))
		body := w.Body.String()
		Expect(body).To(ContainSubstring("good"))
		Expect(body).To(ContainSubstring("broken"))
	})

	It("includes plugin files embedded into the dashboard's build output alongside PluginsDir", func() {
		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, "plugin-runtime.js"), []byte("console.log('runtime')"), 0o644)).To(Succeed())

		distFS := fstest.MapFS{
			"index.html":      {Data: []byte("<html></html>")},
			"favicon.png":     {Data: []byte("not-a-real-png")},
			"plugin-build.js": {Data: []byte("console.log('build')")},
			"assets/index.js": {Data: []byte("console.log('should not be picked up')")},
		}

		router := newPluginsRouterWithDistFS(dir, distFS)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/plugins.js", nil))

		Expect(w.Code).To(Equal(http.StatusOK))
		body := w.Body.String()
		Expect(body).To(ContainSubstring("console.log('build')"))
		Expect(body).To(ContainSubstring("console.log('runtime')"))
		Expect(body).NotTo(ContainSubstring("should not be picked up"))
		Expect(body).NotTo(ContainSubstring("<html>"))

		// Build-time-embedded plugins are written before runtime PluginsDir
		// plugins, so a runtime plugin of the same name registers later and
		// can override one shipped at build time.
		Expect(body).To(MatchRegexp(`(?s)console\.log\('build'\).*console\.log\('runtime'\)`))
	})

	It("produces different ETags for different plugin content", func() {
		dirA := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dirA, "plugin-a.js"), []byte("console.log('a')"), 0o644)).To(Succeed())

		dirB := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dirB, "plugin-a.js"), []byte("console.log('b')"), 0o644)).To(Succeed())

		wsA := &WebServer{PluginsDir: dirA}
		_, etagA, err := wsA.buildPluginsBundle()
		Expect(err).NotTo(HaveOccurred())

		wsB := &WebServer{PluginsDir: dirB}
		_, etagB, err := wsB.buildPluginsBundle()
		Expect(err).NotTo(HaveOccurred())

		Expect(etagA).NotTo(BeEmpty())
		Expect(etagB).NotTo(BeEmpty())
		Expect(etagA).NotTo(Equal(etagB))
	})

	It("fails to build the bundle when a directory is named plugin*.js", func() {
		dir := GinkgoT().TempDir()
		Expect(os.Mkdir(filepath.Join(dir, "plugin-oops.js"), 0o755)).To(Succeed())

		ws := &WebServer{PluginsDir: dir}
		_, _, err := ws.buildPluginsBundle()

		// Documents current behavior (REVIEW.md §3): a directory or unreadable
		// file named plugin*.js aborts the whole bundle build (and, via
		// StartDashboard, dashboard startup) rather than being skipped with a
		// log like a malformed-content file is. This is a known, undecided
		// design gap, not the intended behavior.
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("is a directory"))
	})
})
