# Thunder Citizen

Thunder Citizen brings Thunder Bay's public data together in one place:

- **Transit** — live bus positions, routes, and service performance.
- **Budget** — explore city spending and revenue.
- **Council** — councillors, meeting motions, and recorded votes.

[Visit thundercitizen.ca](https://thundercitizen.ca)

Built with Go, templ, and PostgreSQL/PostGIS.

## Quick start

Requires Docker with Docker Compose.

```bash
git clone https://github.com/thundercitizen/home.git
cd home
docker compose up --build
```

Open [localhost:8080](http://localhost:8080). Database migrations run automatically on startup.

Stop with `Ctrl+C`, then `docker compose down`. Database data is kept between runs.

## Development

From the cloned repository, install the development tools and start the app with hot reload. Requires Go 1.26+, Node.js/npm, Make, and Docker for the database.

Ensure Go's binary directory (`$(go env GOPATH)/bin`) is on your `PATH`. If you used the quick start above, run `docker compose stop app` to free port 8080 first.

```bash
make deps
npm install
docker compose up -d db
make dev
```

Open [localhost:8080](http://localhost:8080). The default database settings match the local Docker database.

Run checks before submitting changes:

```bash
make test    # Generate templates and run Go tests
make lint    # Run Go vet and ESLint
```

## Documentation

- [Development setup and commands](docs/development.md)
- [Production deployment](DEPLOY.md)
- [Architecture](docs/architecture.md)
- [Design system](docs/design-system.md)

## License

[Apache 2.0](LICENSE).
