# Embedded Swagger UI

`swagger-ui-bundle.js` and `swagger-ui.css` are unmodified assets from the
official npm package `swagger-ui-dist`, version **5.34.0**
([upstream](https://github.com/swagger-api/swagger-ui)).
`LICENSE` and `NOTICE` retain the upstream Apache-2.0 attribution.
Both files are embedded in the binary and linked from the UI at
`/swagger-ui/LICENSE` and `/swagger-ui/NOTICE`, including in binary-only
container images. Like all Swagger UI assets, these URLs are unavailable
when documentation is disabled or the production profile is active.
The lockfile pins the package and npm integrity checksum.

To refresh the vendored files from this directory:

```sh
npm ci --ignore-scripts --no-audit --no-fund
cp node_modules/swagger-ui-dist/swagger-ui-bundle.js .
cp node_modules/swagger-ui-dist/swagger-ui.css .
cp node_modules/swagger-ui-dist/LICENSE .
cp node_modules/swagger-ui-dist/NOTICE .
rm -rf node_modules
```

The small HTML page and initializer are sample-owned configuration, not a
replacement UI. The official Swagger UI renders `/api-docs`, sorts operations
by HTTP method, and disables the remote validator. No CDN, external JavaScript,
Node.js runtime, or network access is needed to load the documentation.
