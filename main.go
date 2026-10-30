package main

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	readmePath = "README.md"
	dataPath   = "resources.json"
	maxPerRun  = 250
)

type Resource struct {
	Title       string    `json:"title"`
	URL         string    `json:"url"`
	Source      string    `json:"source"`
	Category    string    `json:"category"`
	Discovered  time.Time `json:"discovered"`
	Description string    `json:"description,omitempty"`
}

type rssDoc struct {
	Channel struct {
		Items []rssItem `xml:"item"`
	} `xml:"channel"`
	Items   []rssItem   `xml:"item"`
	Entries []atomEntry `xml:"entry"`
}
type rssItem struct {
	Title   string `xml:"title"`
	Link    string `xml:"link"`
	Desc    string `xml:"description"`
	PubDate string `xml:"pubDate"`
}
type atomEntry struct {
	Title string `xml:"title"`
	Link  struct {
		Href string `xml:"href,attr"`
	} `xml:"link"`
	Summary string `xml:"summary"`
	Updated string `xml:"updated"`
}

var client = &http.Client{Timeout: 18 * time.Second}

var searchQueries = []string{
	`Django REST framework tutorial`,
	`Django REST framework course training`,
	`Django REST framework YouTube`,
	`Django API authentication JWT permissions`,
	`Django REST framework testing deployment performance`,
	`Django tutorial advanced`,
	`Django REST framework beginner project`,
}

var feeds = []struct{ name, url string }{
	{"Django Blog", "https://www.djangoproject.com/rss/weblog/"},
	{"Real Python", "https://realpython.com/atom.xml"},
}

func main() {
	now := time.Now().UTC()
	resources := loadExisting()
	for _, seed := range curatedSeeds() {
		resources = merge(resources, seed)
	}

	for _, feed := range feeds {
		found, err := fetchFeed(feed.name, feed.url, now)
		if err != nil {
			log.Printf("feed %s: %v", feed.name, err)
			continue
		}
		for _, r := range found {
			resources = merge(resources, r)
		}
	}

	// Google News RSS is public and requires no API key. Search results may
	// include articles, course pages, and video pages indexed by Google.
	for _, q := range searchQueries {
		u := "https://news.google.com/rss/search?q=" + url.QueryEscape(q) +
			"&hl=en-US&gl=US&ceid=US:en"
		found, err := fetchFeed("Google News search: "+q, u, now)
		if err != nil {
			log.Printf("search %q: %v", q, err)
			continue
		}
		for _, r := range found {
			if relevant(r) {
				resources = merge(resources, r)
			}
		}
	}

	// Optional richer web search. Add BRAVE_SEARCH_API_KEY in repository
	// Actions secrets to enable Brave Search API discovery.
	if key := strings.TrimSpace(os.Getenv("BRAVE_SEARCH_API_KEY")); key != "" {
		for _, q := range searchQueries {
			found, err := braveSearch(key, q, now)
			if err != nil {
				log.Printf("Brave search %q: %v", q, err)
				continue
			}
			for _, r := range found {
				if relevant(r) {
					resources = merge(resources, r)
				}
			}
		}
	}

	all := make([]Resource, 0, len(resources))
	for _, r := range resources {
		all = append(all, r)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Category != all[j].Category {
			return all[i].Category < all[j].Category
		}
		if all[i].Discovered.Equal(all[j].Discovered) {
			return strings.ToLower(all[i].Title) < strings.ToLower(all[j].Title)
		}
		return all[i].Discovered.After(all[j].Discovered)
	})
	if len(all) > maxPerRun {
		all = all[:maxPerRun]
	}
	if err := saveJSON(dataPath, all); err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(readmePath, []byte(renderREADME(all, now)), 0644); err != nil {
		log.Fatal(err)
	}
	log.Printf("Updated %s with %d unique resources", readmePath, len(all))
}

func fetchFeed(source, feedURL string, now time.Time) ([]Resource, error) {
	req, err := http.NewRequest(http.MethodGet, feedURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "DjangoDRFRadar/1.0 (+https://github.com/)")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
	if err != nil {
		return nil, err
	}
	var doc rssDoc
	if err := xml.Unmarshal(body, &doc); err != nil {
		return nil, err
	}

	items := doc.Channel.Items
	if len(items) == 0 {
		items = doc.Items
	}
	out := make([]Resource, 0, len(items)+len(doc.Entries))
	for _, item := range items {
		title := cleanText(item.Title)
		link := strings.TrimSpace(item.Link)
		if title == "" || !validURL(link) {
			continue
		}
		out = append(out, Resource{Title: title, URL: normalizeURL(link), Source: source,
			Category: categorize(title + " " + link), Discovered: parseDate(item.PubDate, now),
			Description: truncate(cleanText(item.Desc), 240)})
	}
	for _, entry := range doc.Entries {
		title, link := cleanText(entry.Title), strings.TrimSpace(entry.Link.Href)
		if title == "" || !validURL(link) {
			continue
		}
		out = append(out, Resource{Title: title, URL: normalizeURL(link), Source: source,
			Category: categorize(title + " " + link), Discovered: parseDate(entry.Updated, now),
			Description: truncate(cleanText(entry.Summary), 240)})
	}
	return out, nil
}

func braveSearch(key, query string, now time.Time) ([]Resource, error) {
	u := "https://api.search.brave.com/res/v1/web/search?q=" + url.QueryEscape(query) + "&count=10"
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", key)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %s", resp.Status)
	}
	var payload struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 5<<20)).Decode(&payload); err != nil {
		return nil, err
	}
	out := make([]Resource, 0, len(payload.Web.Results))
	for _, x := range payload.Web.Results {
		if !validURL(x.URL) || strings.TrimSpace(x.Title) == "" {
			continue
		}
		out = append(out, Resource{Title: cleanText(x.Title), URL: normalizeURL(x.URL),
			Source: "Brave Search", Category: categorize(x.Title + " " + x.URL + " " + x.Description),
			Discovered: now, Description: truncate(cleanText(x.Description), 240)})
	}
	return out, nil
}

func loadExisting() map[string]Resource {
	out := map[string]Resource{}
	b, err := os.ReadFile(dataPath)
	if err != nil {
		return out
	}
	var items []Resource
	if json.Unmarshal(b, &items) != nil {
		return out
	}
	for _, r := range items {
		if validURL(r.URL) {
			r.URL = normalizeURL(r.URL)
			out[r.URL] = r
		}
	}
	return out
}
func merge(dst map[string]Resource, r Resource) map[string]Resource {
	r.Title = cleanText(r.Title)
	r.URL = normalizeURL(r.URL)
	if r.Title == "" || !validURL(r.URL) {
		return dst
	}
	if old, ok := dst[r.URL]; ok {
		if old.Description == "" {
			old.Description = r.Description
		}
		if old.Source == "" {
			old.Source = r.Source
		}
		if old.Category == "Other" {
			old.Category = r.Category
		}
		if old.Discovered.IsZero() && !r.Discovered.IsZero() {
			old.Discovered = r.Discovered
		}
		dst[r.URL] = old
		return dst
	}
	if r.Discovered.IsZero() {
		r.Discovered = time.Now().UTC()
	}
	if r.Category == "" {
		r.Category = categorize(r.Title + " " + r.URL)
	}
	dst[r.URL] = r
	return dst
}
func saveJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0644)
}
func renderREADME(items []Resource, now time.Time) string {
	var b strings.Builder
	b.WriteString("# Django & Django REST Framework Learning Radar\n\n")
	b.WriteString("A curated, automatically refreshed directory of documentation, articles, videos, courses, and hands-on training for Django and Django REST Framework (DRF).\n\n")
	b.WriteString("> Updated automatically by GitHub Actions. Discovery uses public RSS/search feeds and is not an exhaustive crawl of the internet. Check the original source for current availability, pricing, and version compatibility.\n\n")
	fmt.Fprintf(&b, "**Last updated:** %s  \n**Unique resources:** %d\n\n", now.Format("2006-01-02 15:04 UTC"), len(items))
	b.WriteString("## Start here\n\n")
	b.WriteString("- [Django official documentation](https://docs.djangoproject.com/)\n")
	b.WriteString("- [Django REST Framework documentation](https://www.django-rest-framework.org/)\n")
	b.WriteString("- [DRF Quickstart](https://www.django-rest-framework.org/tutorial/quickstart/)\n")
	b.WriteString("- [Django Girls tutorial](https://tutorial.djangogirls.org/)\n")
	b.WriteString("- [Go implementation and automation](https://go.dev/doc/)\n\n")
	groups := map[string][]Resource{}
	for _, r := range items {
		groups[r.Category] = append(groups[r.Category], r)
	}
	categories := []string{"Official Documentation", "Videos", "Courses & Training", "Tutorials & Articles", "API Security & Authentication", "Testing & Quality", "Deployment & Performance", "Projects & Examples", "Other"}
	for _, cat := range categories {
		list := groups[cat]
		if len(list) == 0 {
			continue
		}
		fmt.Fprintf(&b, "## %s\n\n", cat)
		for _, r := range list {
			fmt.Fprintf(&b, "- [%s](%s)", markdownText(r.Title), r.URL)
			if r.Description != "" {
				fmt.Fprintf(&b, " — %s", markdownText(r.Description))
			}
			fmt.Fprintf(&b, "  \n  <sub>Source: %s · Discovered: %s</sub>\n", markdownText(r.Source), r.Discovered.Format("2006-01-02"))
		}
		b.WriteString("\n")
	}
	b.WriteString("## How updates work\n\n")
	b.WriteString("A scheduled GitHub Actions workflow runs daily, collects public feed/search results, deduplicates by URL, saves the catalog to `resources.json`, and regenerates this README. Results are best-effort; search providers may rate-limit requests or change their feeds.\n\n")
	b.WriteString("### Optional: richer search\n\n")
	b.WriteString("Set a repository Actions secret named `BRAVE_SEARCH_API_KEY` to enable Brave Search API results. Without it, the workflow uses public RSS/search feeds and the built-in starter list. Review the provider's current terms and pricing before enabling an API.\n\n")
	b.WriteString("## Contributing\n\n")
	b.WriteString("Pull requests adding high-quality, relevant resources are welcome. Please prefer original documentation, explain the resource's value, and avoid duplicate links.\n")
	return b.String()
}

func curatedSeeds() []Resource {
	now := time.Now().UTC()
	raw := []struct{ title, link, category, desc string }{
		{"Django documentation", "https://docs.djangoproject.com/", "Official Documentation", "Official Django documentation, including models, views, forms, security, and deployment."},
		{"Django REST Framework documentation", "https://www.django-rest-framework.org/", "Official Documentation", "Official API guide and tutorials for serializers, views, authentication, permissions, and more."},
		{"DRF Tutorial: Quickstart", "https://www.django-rest-framework.org/tutorial/quickstart/", "Tutorials & Articles", "Build a simple API using serializers, viewsets, and routers."},
		{"DRF Tutorial: Serialization", "https://www.django-rest-framework.org/tutorial/1-serialization/", "Tutorials & Articles", "Start the official multi-part API tutorial."},
		{"Django security documentation", "https://docs.djangoproject.com/en/stable/topics/security/", "API Security & Authentication", "Official guidance for common Django security concerns."},
		{"Django deployment checklist", "https://docs.djangoproject.com/en/stable/howto/deployment/checklist/", "Deployment & Performance", "Production deployment checklist."},
		{"Django Girls Tutorial", "https://tutorial.djangogirls.org/", "Courses & Training", "Beginner-friendly, project-based Django course."},
		{"Django for APIs", "https://djangoforapis.com/", "Courses & Training", "Book and learning resources focused on APIs with Django."},
		{"Real Python: Django REST Framework", "https://realpython.com/tutorials/django/", "Tutorials & Articles", "Django tutorials and practical articles."},
		{"DRF testing documentation", "https://www.django-rest-framework.org/api-guide/testing/", "Testing & Quality", "APIRequestFactory, APIClient, and API test patterns."},
		{"DRF authentication documentation", "https://www.django-rest-framework.org/api-guide/authentication/", "API Security & Authentication", "Authentication classes and request authentication."},
		{"DRF permissions documentation", "https://www.django-rest-framework.org/api-guide/permissions/", "API Security & Authentication", "Permission policies and object-level access."},
		{"DRF throttling documentation", "https://www.django-rest-framework.org/api-guide/throttling/", "API Security & Authentication", "Rate-limiting and throttling API requests."},
		{"DRF filtering documentation", "https://www.django-rest-framework.org/api-guide/filtering/", "Tutorials & Articles", "Filtering API querysets."},
		{"DRF pagination documentation", "https://www.django-rest-framework.org/api-guide/pagination/", "Tutorials & Articles", "Pagination styles for API responses."},
		{"Django performance documentation", "https://docs.djangoproject.com/en/stable/topics/performance/", "Deployment & Performance", "Official performance and optimization guidance."},
		{"Django REST Framework source code", "https://github.com/encode/django-rest-framework", "Projects & Examples", "Explore the DRF implementation and issue history."},
	}
	out := make([]Resource, 0, len(raw))
	for _, x := range raw {
		out = append(out, Resource{Title: x.title, URL: x.link, Source: "Curated starter list",
			Category: x.category, Discovered: now, Description: x.desc})
	}
	return out
}
func categorize(s string) string {
	x := strings.ToLower(s)
	switch {
	case strings.Contains(x, "docs.djangoproject.com") || strings.Contains(x, "django-rest-framework.org"):
		return "Official Documentation"
	case strings.Contains(x, "youtube.com") || strings.Contains(x, "youtu.be") || strings.Contains(x, "video") || strings.Contains(x, "watch?v="):
		return "Videos"
	case containsAny(x, "course", "training", "bootcamp", "class", "udemy", "coursera", "pluralsight", "educative", "workshop", "learning path"):
		return "Courses & Training"
	case containsAny(x, "jwt", "oauth", "authentication", "authorization", "permission", "security", "csrf", "token"):
		return "API Security & Authentication"
	case containsAny(x, "testing", "pytest", "test-driven", "coverage", "factory boy", "mock"):
		return "Testing & Quality"
	case containsAny(x, "deploy", "performance", "optimization", "optimisation", "cache", "caching", "docker", "production", "scaling"):
		return "Deployment & Performance"
	case containsAny(x, "github.com", "project", "example", "boilerplate", "repository", "repo", "build an app"):
		return "Projects & Examples"
	case containsAny(x, "tutorial", "guide", "article", "blog", "how to", "serializer", "viewset", "api"):
		return "Tutorials & Articles"
	default:
		return "Other"
	}
}
func relevant(r Resource) bool {
	x := strings.ToLower(r.Title + " " + r.URL + " " + r.Description)
	return containsAny(x, "django", "django rest framework", " drf ", "djangorestframework")
}
func containsAny(s string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}
func validURL(s string) bool {
	u, err := url.ParseRequestURI(strings.TrimSpace(s))
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}
func normalizeURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return raw
	}
	u.Fragment = ""
	q := u.Query()
	for k := range q {
		l := strings.ToLower(k)
		if strings.HasPrefix(l, "utm_") || l == "fbclid" || l == "gclid" || l == "ref" {
			q.Del(k)
		}
	}
	u.RawQuery = q.Encode()
	return strings.TrimRight(u.String(), "/")
}

var tagRE = regexp.MustCompile(`<[^>]*>`)
var spaceRE = regexp.MustCompile(`\s+`)

func cleanText(s string) string {
	s = tagRE.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, "\u00a0", " ")
	return strings.TrimSpace(spaceRE.ReplaceAllString(s, " "))
}
func markdownText(s string) string {
	s = strings.ReplaceAll(s, "[", "\\[")
	s = strings.ReplaceAll(s, "]", "\\]")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
func parseDate(s string, fallback time.Time) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return fallback
	}
	for _, layout := range []string{time.RFC1123Z, time.RFC1123, time.RFC3339, "Mon, 02 Jan 2006 15:04:05 -0700", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return fallback
}
