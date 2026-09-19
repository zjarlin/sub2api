# 252 Cluster Deployment

The stable client endpoint remains `192.168.31.252:18080`. Nginx owns that
port and forwards to two `sub2api` containers on the Compose network. The
PostgreSQL and Redis services remain shared, so JWT secrets, scheduler state,
and account data must continue to come from the existing `/opt/sub2api/.env`.

This protects against an application-container crash, not a failure of the
252 host, Docker, its network interface, or the Cloudflare Tunnel. Host-level
redundancy requires a second host and a floating VIP or load balancer.

The first deployment migrates the existing direct `sub2api:18080` listener to
the gateway and can have a short cutover window. Later deployments keep the
gateway and replace the application replicas behind it.

The pipeline starts one application replica first, verifies the gateway
health endpoint, and only then scales to two replicas. This is a canary health
gate, not percentage traffic splitting: the first replica receives all traffic
during the short probe window. A failed probe restores the previous image.
