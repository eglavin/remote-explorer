# HTTP request examples

Ready-to-run requests for every endpoint, in the `.http` format understood by:

- **VS Code** with the [REST Client](https://marketplace.visualstudio.com/items?itemName=humao.rest-client) extension: click **Send Request** above a request.
- **JetBrains IDEs** (IntelliJ IDEA, GoLand, …) with the built-in HTTP Client: click the ▶ icon next to a request.

| File                                               | Covers                                                               |
| -------------------------------------------------- | -------------------------------------------------------------------- |
| [01-health-and-info.http](01-health-and-info.http) | `GET /healthz`, `GET /api/info`                                      |
| [02-upload.http](02-upload.http)                   | `POST /api/upload`: single and multiple files, `mkdirs`, `overwrite` |
| [03-list.http](03-list.http)                       | `GET /api/list` for the root and subfolders                          |
| [04-download.http](04-download.http)               | `GET`/`HEAD /api/download`, ranges, conditional requests             |
| [05-errors.http](05-errors.http)                   | Auth failures, path escapes and other error responses                |

## Setup

1. Start the server. `--write` is needed for the upload examples, and `--overwrite` for the overwrite example:

   ```bash
   remote-explorer --write --overwrite ./some-folder
   ```

2. Copy the printed access token into the `@token` line at the top of each file. If you run the server with `--no-auth`, any value works.

3. If the server is not on `https://127.0.0.1:8080`, change `@baseUrl` too.

4. The server's self-signed certificate is new on every run and no client trusts it. If your client rejects it, turn off certificate verification for this server in the client's settings, or start the server with `--no-tls` and change `@baseUrl` to `http://`.

The files are numbered in a sensible order. `02-upload.http` creates `examples-demo/hello.txt` in the served folder, which the list and download examples then use. Each file notes the status it expects.

The sample files that get uploaded are in [`files/`](files).
