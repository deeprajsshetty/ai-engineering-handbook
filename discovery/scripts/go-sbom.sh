#!/usr/bin/env bash
# Emits a CycloneDX 1.5 JSON component list of the Go modules linked into the
# server binary (from `go list -deps`), with licenses detected from each
# module's LICENSE file. CI additionally produces an SPDX SBOM of the image
# with Syft; this script is the offline, no-extra-tool variant.
set -euo pipefail
cd "$(dirname "$0")/../platform"
python3 -I - "$(go env GOMODCACHE)" <<'PY'
import json, subprocess, sys, os, uuid, datetime
sys.path.insert(0, os.path.join(os.getcwd(), "..", "scripts"))
cache = sys.argv[1]
out = subprocess.check_output(["go", "list", "-deps", "-f", "{{if .Module}}{{.Module.Path}}@{{.Module.Version}}{{end}}", "./cmd/obsplatform"], text=True)
mods = sorted({l for l in out.split() if "@" in l and not l.endswith("@")})
def detect(path):
    for n in ("LICENSE", "LICENSE.txt", "LICENSE.md", "COPYING", "LICENCE"):
        p = os.path.join(path, n)
        if os.path.exists(p):
            t = open(p, errors="ignore").read()
            if "Apache License" in t and "Version 2.0" in t: return "Apache-2.0"
            if "Permission is hereby granted, free of charge" in t: return "MIT"
            if "Redistribution and use in source and binary forms" in t:
                return "BSD-3-Clause" if "Neither the name" in t or "names of its" in t else "BSD-2-Clause"
            if "Mozilla Public License" in t: return "MPL-2.0"
            if "Permission to use, copy, modify, and/or distribute" in t: return "ISC"
            return "UNKNOWN (see LICENSE)"
    return "NOT FOUND"
comps = []
for m in mods:
    path, ver = m.rsplit("@", 1)
    esc = "".join("!" + c.lower() if c.isupper() else c for c in path)
    comps.append({"type": "library", "name": path, "version": ver, "purl": f"pkg:golang/{path}@{ver}",
                  "licenses": [{"license": {"name": detect(os.path.join(cache, f"{esc}@{ver}"))}}]})
print(json.dumps({"bomFormat": "CycloneDX", "specVersion": "1.5", "serialNumber": f"urn:uuid:{uuid.uuid4()}",
  "metadata": {"timestamp": datetime.datetime.now(datetime.timezone.utc).isoformat(), "component": {"type": "application", "name": "obsplatform"}},
  "components": comps}, indent=2))
PY
