# AxonWall ISO build container — pinned for reproducible image-factory builds.
#
# Base image pinned BY DIGEST to one instant of Debian 13 "trixie" (point release
# 13.7): the digest is the multi-arch OCI index of the `trixie-20261005` date tag,
# resolved from registry-1.docker.io on 2026-10-09. It is pulled from
# mirror.gcr.io — Google's read-through cache of Docker Hub official images,
# which serves the same upstream manifest (same digest) without Docker Hub's
# unauthenticated pull rate limit that 429s shared GitHub runner IPs (first CI
# run failed exactly that way on 2026-10-09). To move the pin: re-resolve the
# digest, update the guard below, and note the new point release in
# build/PACKAGE-VERIFY.md.
FROM mirror.gcr.io/library/debian@sha256:913f6706df59a68922d1dd08f78c2476560a8d367897200a6005b00e5f67c2d5

# Guard: fail loudly if the pinned digest is not the expected trixie point release.
RUN test "$(cat /etc/debian_version)" = "13.7" \
	|| { echo "FATAL: pinned base image is not trixie 13.7 — re-pin the digest" >&2; exit 1; }

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update \
	&& apt-get install -y --no-install-recommends \
		ca-certificates \
		curl \
		git \
		live-build \
		mtools \
		wget \
		xorriso \
	&& rm -rf /var/lib/apt/lists/*

# Resilience for the pinned snapshot mirror (snapshot.debian.org rate-limits bursts).
COPY 80-axonwall-retries.conf /etc/apt/apt.conf.d/80-axonwall-retries

WORKDIR /repo
CMD ["/bin/bash"]
