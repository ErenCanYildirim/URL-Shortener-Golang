# URL-Shortener-Golang

Production ready URL-Shortener service built with golang.

If you want to run it locally as a playground then switch to the local branch, it uses sqlite.

The main branch uses postgresql with connection pooling to ensure prod-grade stability.

How to run:
    1. Create an .env file with POSTGRES_PASSWORD, POSTGRES_PORT, APP_PORT
    2. Start with ```bash
        docker-compose up --build
    ```

Missing:
    -> Transaction safety for some db-operations