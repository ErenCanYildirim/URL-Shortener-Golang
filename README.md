# URL-Shortener-Golang

Production ready URL-Shortener service built with golang.

If you want to run it locally as a playground then switch to the local branch, it uses sqlite.

The main branch uses postgresql with connection pooling to ensure prod-grade stability.

How to run:
    1. Create an .env file with POSTGRES_PASSWORD, POSTGRES_PORT, APP_PORT
    2. Start with ```bash
        docker-compose up --build
    ```

Notes:

1. asynchronous background tasks for analytics on url clicks (frequency etc.)
2. in-memory cache for frequent URLs (change to a Redis cache for a more performant service)
3. better algorithm for a short code generation

API:

**HTTP Method:** `GET`
**URL Path:** `/health`

**HTTP Method:** `GET`
**URL Path:** `/api/stats/{shortCode}`

**HTTP Method:** `GET`
**URL Path:** `/api/list`

**HTTP Method:** `GET`
**URL Path:** `/{shortCode}`

**HTTP Method:** `POST`
**URL Path:** `/api/shorten`

Missing:
    -> Transaction safety for some db-operations