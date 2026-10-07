# Copyright Monte Carlo AI, Inc.
# SPDX-License-Identifier: Apache-2.0
"""Serves a GoReleaser dist/ the way GitHub serves a repository's releases, for the installer
tests: <base>/latest redirects to <base>/tag/<tag>, and <base>/download/<tag>/<file> is a file
from dist/. With --no-release, latest redirects to the releases page itself, as GitHub's does
before a repository's first release.

Prints the base URL, then serves until killed.
"""

import argparse
import http.server
import os
import sys


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--dist", required=True)
    parser.add_argument("--tag", required=True)
    parser.add_argument("--no-release", action="store_true")
    args = parser.parse_args()
    dist = os.path.abspath(args.dist)

    class Handler(http.server.BaseHTTPRequestHandler):
        def do_HEAD(self) -> None:
            self.respond(body=False)

        def do_GET(self) -> None:
            self.respond(body=True)

        def respond(self, body: bool) -> None:
            base = f"http://127.0.0.1:{self.server.server_port}"
            if self.path == "/latest":
                self.send_response(302)
                self.send_header("Location", base if args.no_release else f"{base}/tag/{args.tag}")
                self.end_headers()
                return
            if self.path == "" or self.path == "/" or (self.path == f"/tag/{args.tag}" and not args.no_release):
                self.send_response(200)
                self.send_header("Content-Length", "0")
                self.end_headers()
                return
            prefix = f"/download/{args.tag}/"
            name = self.path[len(prefix):] if self.path.startswith(prefix) else ""
            path = os.path.join(dist, name)
            if not name or "/" in name or not os.path.isfile(path):
                self.send_error(404)
                return
            with open(path, "rb") as f:
                data = f.read()
            self.send_response(200)
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            if body:
                self.wfile.write(data)

        def log_message(self, *_: object) -> None:
            pass

    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    print(f"http://127.0.0.1:{server.server_port}", flush=True)
    sys.stdout.close()
    server.serve_forever()


if __name__ == "__main__":
    main()
