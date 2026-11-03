# Worker Kit

`danielmichaels-worker-kit-0.1.1.tgz` is the built package from
`danielmichaels/worker-kit` commit
`5c1bf5d3dced8bdccbf72046aa3e552d073cf515`.

SHA256: `260ddf0326044b55b888546aa6af51da9f9a5fd2063d9fd4ebf62c34e658f41a`.

The package is intentionally vendored rather than fetched from npm. It was built
from the upstream locked dependencies with `pnpm install --frozen-lockfile`,
`pnpm build`, then `npm pack --ignore-scripts`. Calendar Digest imports
`@danielmichaels/worker-kit/access`; npm installs this archive through the root
lockfile. Fresh orbs need no GitHub clone or separate kit build.

The archive lives under `worker/vendor/`, not the repository root's `vendor/`,
because a root vendor directory changes how the retained Go app resolves modules.

To upgrade, build a reviewed upstream revision, replace the archive, update its
provenance and lockfile, then run the consumer's typecheck, workerd tests and
dry-run build. Never change the verifier inside the archive locally.
