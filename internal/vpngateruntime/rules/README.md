# Bundled domain rule snapshots

The binary rule resources are generated locally and excluded from Git. Before
running Go build or test commands in a clean checkout, prepare the files required
by `bundled_rules.go`:

```powershell
./scripts/prepare-rule-resources.ps1
```

The Windows build script and verification workflow run this step automatically.
The script reuses matching local files without network access. Otherwise it
downloads to a temporary file, verifies SHA256, and only then replaces the local
resource. A failed download or checksum check preserves the existing file.
The script only writes this source resource directory; it does not update rules
or settings in an application data directory.

Both snapshots come from the `sing` branch of
[MetaCubeX/meta-rules-dat](https://github.com/MetaCubeX/meta-rules-dat), pinned to
[commit 231248b8e335758254deb15462cba5e2337d1e21](https://github.com/MetaCubeX/meta-rules-dat/commit/231248b8e335758254deb15462cba5e2337d1e21)
(2026-11-17). These are domain lists, not GeoIP routing rules.

| Resource | Bytes | SHA256 |
| --- | ---: | --- |
| [cn.srs](https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/231248b8e335758254deb15462cba5e2337d1e21/geo/geosite/cn.srs) | 440027 | `0e8a3bf62266fedd8b4dd7c11bb0bfb16ae34e86728b747179411e0dfbdcf1cb` |
| [gfw.srs](https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/231248b8e335758254deb15462cba5e2337d1e21/geo/geosite/gfw.srs) | 28772 | `ce4eb8aa6b2771f4499b3edba097f5310c5512bcde47ddedef301d0d2d23ddfc` |

Provider and source attribution remain with MetaCubeX/meta-rules-dat and the
contributors credited in its [README](https://github.com/MetaCubeX/meta-rules-dat#致谢).
The provider publishes a [GPL-3.0 license](https://github.com/MetaCubeX/meta-rules-dat/blob/master/LICENSE);
this repository includes the GPL-3.0 text in [LICENSE](../../../LICENSE).

At runtime, rule downloads use the configured external mixed proxy. Updates are
validated with the pinned sing-box rule-set parser before atomic replacement;
the last valid copy is retained.
