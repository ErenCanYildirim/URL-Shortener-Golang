# URL-Shortener-Golang

A production-ready URL shortener service built with Go, PostgreSQL and Redis.

For local development or experimentation, switch to the local branch — it uses SQLite for simplicity. The main branch is optimized for production with PostgreSQL and connection pooling to ensure reliability and performance.

# How to run

1. Create a ```.env```file with the following vars: `POSTGRES_PASSWORD`, `POSTGRES_PORT` and `APP_PORT`.
2. Launch service with `docker-compose up --build`.

# API Endpoints

`GET /health` — Check service health

`GET /api/stats/{shortCode}` — Retrieve stats for a shortened URL

`GET /api/list` — List all shortened URLs

`GET /{shortCode}` — Redirect to the original URL

`POST /api/shorten` — Create a new shortened URL

# Future extensions

Add transaction safety for critical database operations

Improve short code generation algorithm for uniqueness and efficiency