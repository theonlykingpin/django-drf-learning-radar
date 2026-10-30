# Django & Django REST Framework Learning Radar

A curated, automatically refreshed directory of documentation, articles, videos, courses, and hands-on training for Django and Django REST Framework (DRF).

This repository is bootstrapped by the Go program. Run `go run .` to discover resources and regenerate this README.

## Start here

- [Django official documentation](https://docs.djangoproject.com/)
- [Django REST Framework documentation](https://www.django-rest-framework.org/)
- [DRF Quickstart](https://www.django-rest-framework.org/tutorial/quickstart/)
- [Django Girls tutorial](https://tutorial.djangogirls.org/)

## Daily updates

GitHub Actions runs the crawler daily and commits README/catalog changes. Search results depend on public feeds and provider coverage; this is not an exhaustive crawl of the internet.

To enable optional Brave Search API results, add a repository Actions secret named `BRAVE_SEARCH_API_KEY`. Without it, the crawler uses public RSS/search feeds and a built-in curated starter list.

## Run locally

```bash
go run .
```
