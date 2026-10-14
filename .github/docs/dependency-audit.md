# Dependency audit maintenance

The workspace includes `../packages/@nself/*`. Check out the packages commit
pinned in `.github/workflows/ci.yml` beside this repository before regenerating
`pnpm-lock.yaml`. Use pnpm 10, matching Desktop CI.

## D-0675 baseline and fix (2026-10-13)

On main `3ad5491`, `pnpm audit --audit-level=high` failed with four high
findings and no critical findings. The full report contained two low and seven
moderate findings as well.

| Package | Severity | Advisory | Resolution |
| --- | --- | --- | --- |
| image-size | high | GHSA-5p2g-fcmc-qvqq, GHSA-w3rx-r6r6-pgpr | Metro 0.83.8 removes the dependency and fixes its image parsers. |
| node-forge | high | GHSA-86w9-cpqp-85rv | No published patched version; existing exception retained using GHSA ID. |
| braces | high | GHSA-vfj7-8cjw-p6xm | No published patched version; existing exception retained using GHSA ID. |
| react-router | moderate | GHSA-wrjc-x8rr-h8h6, GHSA-337j-9hxr-rhxg | Remains outside the high-severity slice. |
| dompurify | moderate | GHSA-55q2-fjhq-7xh7 | Remains outside the high-severity slice. |
| decode-uri-component | moderate | GHSA-vcc3-ghjq-m6fr | Remains outside the high-severity slice. |
| react-router-dom | moderate | GHSA-jjmj-jmhj-qwj2 | Remains outside the high-severity slice. |
| markdown-it | moderate | GHSA-253c-mchw-3w2r | Remains outside the high-severity slice. |
| js-yaml | moderate | GHSA-r3ph-w7gj-g6xm | Remains outside the high-severity slice. |
| dompurify | low | GHSA-c2j3-45gr-mqc4, GHSA-6688-9rhm-gjv2 | Remains outside the high-severity slice. |

The registry records omit CVE IDs, so the previous CVE exceptions did not
match. The two image-size exceptions are removed; the two exceptions without
published fixes use their equivalent GHSA IDs. Remove those exceptions as
soon as compatible patched releases are available.

Do not override image-size 1.x directly to 2.x: Metro 0.82 calls the CommonJS
export as a function, whereas 2.x exports an object. The Metro override raises
that parent instead. Keep undici bounded to 7.x for jsdom 29.

After the change, the high-severity audit exits zero: two low and seven moderate
findings remain, plus the same two high exceptions without published fixes.
