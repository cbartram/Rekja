<!-- PROJECT LOGO -->
<br />
<div align="center">

<h3 align="center">Rekja</h3>

  <p align="center">
    A terminal UI for managing Thunderstore Valheim mods on a Kubernetes-hosted ValheimPlus server.
    <br />
  </p>
</div>

[![Contributors][contributors-shield]][contributors-url]
[![Forks][forks-shield]][forks-url]
[![Stargazers][stars-shield]][stars-url]
[![Issues][issues-shield]][issues-url]
[![Go Version](https://img.shields.io/github/go-mod/go-version/cbartram/Rekja?style=for-the-badge)](go.mod)

---

# Getting Started

Rekja is a Bubble Tea terminal application for managing additional Thunderstore/BepInEx-style plugins on a Kubernetes-hosted dedicated Valheim server.

It is designed for the `ghcr.io/community-valheim-tools/valheim-server` container with `VALHEIM_PLUS=true`. ValheimPlus itself, including its bundled BepInEx runtime and image-managed update behavior, remains owned by the server image. Rekja manages only extra plugins layered under:

```text
/config/valheimplus/plugins/
```

Use at your own risk. Mod updates can change server behavior, break world compatibility, or require matching client-side plugin changes.

## Quick Start

Clone and verify the project:

```shell
git clone https://github.com/cbartram/Rekja
cd ./Rekja

go test ./...
go run ./cmd/rekja
```

For local development outside the Valheim Pod, point Rekja at a temporary plugin directory:

```shell
export REKJA_PLUGINS_DIR=/tmp/rekja-valheim/plugins
go run ./cmd/rekja
```

## What Rekja Manages

Rekja treats Thunderstore as the source of truth for available mod packages and versions. It queries the Valheim community package index at:

```text
https://valheim.thunderstore.io/api/v1/package/
```

The tool can:

- Inventory tracked and untracked files under `/config/valheimplus/plugins/`
- Maintain an installed manifest at `/config/valheimplus/.rekja/installed.json`
- Compare installed tracked mods against Thunderstore versions
- Resolve Thunderstore dependency declarations transitively
- Download, stage, and extract package zips into the ValheimPlus plugin directory
- Preserve existing config files during updates
- Refuse to overwrite untracked files or files owned by another tracked package
- Read Kubernetes logs and restart the Valheim server process with `supervisorctl restart valheim-server`

Rekja does not manage ValheimPlus itself. The server image's `UPDATE_CRON` behavior remains separate.

## TUI Usage

Run the app:

```shell
go run ./cmd/rekja
```

Default key bindings:

```text
1 installed
2 updates
3 dependencies
4 sync
5 logs
6 config
a add tracked mod
d untrack selected mod
r refresh inventory and Thunderstore index
p build dependency plan
s sync current plan
l load server logs
R restart valheim-server
q quit
```

Tracked mods are entered as:

```text
Namespace-Name
Namespace-Name@1.2.3
```

For example:

```text
ValheimModding-Jotunn
Azumatt-AzuCraftyBoxes@1.5.3
```

Untracking a mod removes it from Rekja's manifest only. It does not delete plugin files.

### Prerequisites

- [Go](https://go.dev/) 1.26+
- [Git](https://git-scm.com/)
- A Kubernetes-hosted Valheim server running `ghcr.io/community-valheim-tools/valheim-server`
- `VALHEIM_PLUS=true`
- A mounted `/config` PVC containing `/config/valheimplus/plugins/`
- Kubernetes RBAC allowing Pod discovery, log reads, and exec if restart/log features are used

## Runtime Shape

The recommended deployment model is a sidecar in the same Pod as the Valheim server:

```text
Valheim Pod
|-- valheim-server container
|   `-- /config  -> PVC
`-- rekja container
    `-- /config  -> same PVC
```

This is intentional. Many game-server PVCs are `ReadWriteOnce`, so a sidecar avoids multi-attach problems and avoids copying files through the Kubernetes API.

Rekja writes only under:

```text
/config/valheimplus/plugins/
/config/valheimplus/.rekja/
```

## Configuration

Rekja can run from defaults, environment variables, or a JSON config file:

```shell
go run ./cmd/rekja -config ./rekja.json
```

Example config:

```json
{
  "plugins_dir": "/config/valheimplus/plugins",
  "thunderstore_base_url": "https://valheim.thunderstore.io",
  "kubernetes": {
    "namespace": "valheim",
    "label_selector": "app=valheim",
    "container_name": "valheim-server",
    "restart_command": ["supervisorctl", "restart", "valheim-server"]
  },
  "tracked": [
    {
      "namespace": "ValheimModding",
      "name": "Jotunn",
      "full_name": "ValheimModding-Jotunn",
      "desired_version": "latest"
    }
  ]
}
```

Environment overrides:

- `REKJA_PLUGINS_DIR`
- `REKJA_THUNDERSTORE_BASE_URL`
- `REKJA_NAMESPACE`
- `REKJA_POD_NAME`
- `REKJA_LABEL_SELECTOR`
- `REKJA_CONTAINER`
- `KUBECONFIG`

## Installed Manifest

Rekja stores package ownership and file hashes in:

```text
/config/valheimplus/.rekja/installed.json
```

The manifest maps installed files back to Thunderstore package identity and version:

```json
{
  "schema_version": 1,
  "target": {
    "plugins_dir": "/config/valheimplus/plugins",
    "valheimplus_config": "/config/valheimplus/valheim_plus.cfg"
  },
  "tracked": [
    {
      "namespace": "ValheimModding",
      "name": "Jotunn",
      "full_name": "ValheimModding-Jotunn",
      "desired_version": "latest",
      "installed_version": "2.24.3",
      "source": "thunderstore",
      "dependency_mode": "auto-approved",
      "files": [
        {
          "path": "Jotunn.dll",
          "sha256": "...",
          "kind": "plugin"
        }
      ]
    }
  ]
}
```

DLL assembly metadata is not treated as the source of truth because Thunderstore packages can contain multiple DLLs, configs, assets, and dependency metadata. Rekja uses its manifest and file hashes for ownership and drift detection.

## Development

Run tests:

```bash
go test ./...
```

Run vet:

```bash
go vet ./...
```

Run the TUI locally:

```bash
export REKJA_PLUGINS_DIR=/tmp/rekja-valheim/plugins
go run ./cmd/rekja
```

## Project Layout

```text
cmd/rekja              CLI entrypoint
internal/app           Application orchestration
internal/config        Runtime config loading
internal/inventory     Plugin directory scanning and manifest persistence
internal/kube          Kubernetes pod/log/exec client
internal/manifest      Installed manifest data model
internal/resolve       Thunderstore dependency resolver
internal/sync          Download, staging, extraction, and manifest updates
internal/thunderstore  Thunderstore API client and version parsing
internal/tui           Bubble Tea model, update loop, and views
```

## Safety Model

Rekja is intentionally conservative around file writes:

- Package zips are staged before extraction
- Archive traversal paths are rejected
- Known Thunderstore layout roots such as `plugins/` and `BepInEx/plugins/` are normalized
- Untracked files block overwrite
- Files owned by another tracked package block overwrite
- Config-like files are preserved on update by default
- Server restarts are explicit TUI actions

## Built With

* [Go](https://go.dev/) - Core language
* [Bubble Tea v2](https://charm.land/) - Terminal UI framework
* [Bubbles v2](https://charm.land/) - TUI components
* [Lip Gloss v2](https://charm.land/) - Terminal styling
* [client-go](https://github.com/kubernetes/client-go) - Kubernetes API access
* [Thunderstore](https://valheim.thunderstore.io/) - Valheim mod package source
* [community-valheim-tools/valheim-server-docker](https://github.com/community-valheim-tools/valheim-server-docker) - Target server image family

---

## Contributing

1. Create a new branch from `master`
2. Keep changes scoped to one behavior or subsystem
3. Add focused tests for new resolver, sync, inventory, or config behavior
4. Run `go test ./...` and `go vet ./...`
5. Commit with a clear message, for example `feat(sync): preserve plugin config files`
6. Open a Pull Request

---

## Versioning

Rekja should use [Semantic Versioning](https://semver.org/) once releases begin.

Breaking manifest schema changes should increment the major version or include an explicit migration path.

---

## License

This project is licensed under the [GNU General Public License 3.0](LICENSE).

---

## Acknowledgments

* **Charm** - For Bubble Tea, Bubbles, and Lip Gloss
* **Thunderstore** - For public Valheim mod package metadata and downloads
* **community-valheim-tools** - For the Valheim server container this tool targets
* **ValheimPlus and BepInEx maintainers** - For the plugin runtime Rekja layers onto

[contributors-shield]: https://img.shields.io/github/contributors/cbartram/Rekja.svg?style=for-the-badge
[contributors-url]: https://github.com/cbartram/Rekja/graphs/contributors
[forks-shield]: https://img.shields.io/github/forks/cbartram/Rekja.svg?style=for-the-badge
[forks-url]: https://github.com/cbartram/Rekja/network/members
[stars-shield]: https://img.shields.io/github/stars/cbartram/Rekja.svg?style=for-the-badge
[stars-url]: https://github.com/cbartram/Rekja/stargazers
[issues-shield]: https://img.shields.io/github/issues/cbartram/Rekja.svg?style=for-the-badge
[issues-url]: https://github.com/cbartram/Rekja/issues
