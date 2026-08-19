# Statsig signer relay

Internal-only relay for Grok2API `StatsigMode=url`. It obtains matching
Cloudflare clearance and User-Agent values from FlareSolverr and forwards the
existing signer request to `grok.wodf.de`. An upstream 403 refreshes clearance
once and retries. The relay accepts only `POST /sign`, limits payloads, and
never logs request bodies, cookies, page metadata, or signatures.
