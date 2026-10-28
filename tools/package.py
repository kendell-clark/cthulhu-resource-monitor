# Copyright (c) 2026 Storm Dragon
# SPDX-License-Identifier: GPL-3.0-or-later
"""Build reproducible Tentacle release artifacts and a catalog for review."""

import argparse
import gzip
import hashlib
import io
import json
import os
from pathlib import Path
import subprocess
import tarfile
import tomllib
from urllib.parse import urlparse


def build_package(repoRoot, architecture, version, name):
    sourceRoot = repoRoot / name
    manifest = tomllib.loads((sourceRoot / "tentacle.toml").read_text())
    binaryName = manifest["entry-point"][0]
    distRoot = repoRoot / "dist"
    distRoot.mkdir(exist_ok=True)
    programPath = distRoot / f"{name}-linux-{architecture}"
    environment = dict(os.environ, CGO_ENABLED="1", GOOS="linux", GOARCH=architecture)
    environment.setdefault("GOCACHE", "/tmp/cthulhu-cache")
    subprocess.run(["go", "build", "-buildvcs=false", "-mod=readonly", "-trimpath",
                    "-ldflags=-s -w", "-o", str(programPath), f"./{name}/src"],
                   cwd=repoRoot, env=environment, check=True)
    artifactPath = distRoot / f"{name}-{version}-linux-{architecture}.tar.gz"
    with artifactPath.open("wb") as output:
        with gzip.GzipFile(filename="", mode="wb", fileobj=output, mtime=0) as compressed:
            with tarfile.open(fileobj=compressed, mode="w", format=tarfile.USTAR_FORMAT) as archive:
                for name, data, mode in [
                    ("tentacle.toml", (sourceRoot / "tentacle.toml").read_bytes(), 0o644),
                    ("README.md", (sourceRoot / "README.md").read_bytes(), 0o644),
                    ("LICENSE", (sourceRoot / "LICENSE").read_bytes(), 0o644),
                    (binaryName, programPath.read_bytes(), 0o755),
                ]:
                    member = tarfile.TarInfo(name)
                    member.size, member.mode, member.mtime = len(data), mode, 0
                    archive.addfile(member, io.BytesIO(data))
    digest = hashlib.sha256(artifactPath.read_bytes()).hexdigest()
    return artifactPath, digest


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--architecture", nargs="+", choices=["amd64", "arm64"], default=["amd64", "arm64"])
    parser.add_argument("--tentacle", nargs="+", help="Catalog IDs to package (default: hello-world)")
    parser.add_argument("--download-base", help="Immutable HTTPS directory containing the archives")
    args = parser.parse_args()
    if args.download_base:
        parsed = urlparse(args.download_base)
        if parsed.scheme != "https" or not parsed.hostname or parsed.username or parsed.fragment or parsed.query:
            parser.error("Download base must be an HTTPS directory without credentials, query or fragment")
    if len(set(args.architecture)) != len(args.architecture):
        parser.error("Architectures must not be repeated")
    repoRoot = Path(__file__).resolve().parents[1]
    catalog = tomllib.loads((repoRoot / "catalog.toml").read_text())
    names = [item["id"] for item in catalog["tentacles"]]
    selected = args.tentacle or ["resource-monitor"]
    if len(set(selected)) != len(selected) or any(name not in names for name in selected):
        parser.error("Select distinct Tentacle IDs from catalog.toml")
    lines = ["format-version = 1"]
    checksums = []
    for item in catalog["tentacles"]:
        name = item["id"]
        lines += ["", "[[tentacles]]", f"id = {json.dumps(name)}",
                  f"manifest = {json.dumps(item['manifest'])}"]
        releases = item.get("releases", [])
        if name in selected:
            manifest = tomllib.loads((repoRoot / item["manifest"]).read_text())
            version = manifest["version"]
            if not version or any(character not in "0123456789abcdefghijklmnopqrstuvwxyz.-" for character in version):
                raise ValueError("Version must be safe for release filenames")
            releases = []
            for architecture in args.architecture:
                artifactPath, digest = build_package(repoRoot, architecture, version, name)
                releaseURL = (args.download_base.rstrip("/") + "/" + artifactPath.name) if args.download_base else f"https://github.com/kendell-clark/cthulhu-resource-monitor/releases/download/{name}-v{version}/{artifactPath.name}"
                releases.append(dict(platform="linux", architecture=architecture, url=releaseURL, sha256=digest))
                checksums.append(f"{digest}  {artifactPath.name}")
                print(f"Prepared {artifactPath.name}: {digest}")
        for release in releases:
            lines += ["", "[[tentacles.releases]]"]
            lines += [f"{key} = {json.dumps(release[key])}" for key in ("platform", "architecture", "url", "sha256")]
    (repoRoot / "dist/catalog.toml").write_text("\n".join(lines) + "\n")
    (repoRoot / "dist/SHA256SUMS").write_text("\n".join(checksums) + "\n")
    print("Prepared dist/catalog.toml and dist/SHA256SUMS for review. Nothing was published.")


if __name__ == "__main__":
    main()
