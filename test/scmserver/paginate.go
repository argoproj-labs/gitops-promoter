package scmserver

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// pageParams reads the 1-based "page" and the page size query parameters, applying the provider's default and
// maximum page size.
func pageParams(r *http.Request, sizeKey string, defaultSize, maxSize int) (page, size int) {
	page, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || page < 1 {
		page = 1
	}
	size, err = strconv.Atoi(r.URL.Query().Get(sizeKey))
	if err != nil || size <= 0 {
		size = defaultSize
	}
	return page, min(size, maxSize)
}

// pageWindow returns the [start, end) indexes of page in a list of total items.
func pageWindow(total, page, size int) (start, end int) {
	start = min((page-1)*size, total)
	end = min(start+size, total)
	return start, end
}

// lastPage returns the number of pages, at least 1.
func lastPage(total, size int) int {
	if total == 0 {
		return 1
	}
	return (total + size - 1) / size
}

// pageURL returns the request URL with the "page" parameter replaced.
func pageURL(r *http.Request, page int) string {
	u := url.URL{Scheme: "https", Host: r.Host, Path: r.URL.Path}
	q := r.URL.Query()
	q.Set("page", strconv.Itoa(page))
	u.RawQuery = q.Encode()
	return u.String()
}

// setLinkHeader writes an RFC 8288 Link header with next, prev, first and last relations, as GitHub, GitLab, Gitea
// and Forgejo do.
func setLinkHeader(w http.ResponseWriter, r *http.Request, page, last int) {
	var links []string
	if page < last {
		links = append(links, fmt.Sprintf(`<%s>; rel="next"`, pageURL(r, page+1)))
	}
	if page > 1 {
		links = append(links, fmt.Sprintf(`<%s>; rel="prev"`, pageURL(r, page-1)))
	}
	if last > 1 {
		links = append(links,
			fmt.Sprintf(`<%s>; rel="first"`, pageURL(r, 1)),
			fmt.Sprintf(`<%s>; rel="last"`, pageURL(r, last)))
	}
	if len(links) > 0 {
		w.Header().Set("Link", strings.Join(links, ", "))
	}
}
