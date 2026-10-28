#!/usr/bin/env bash
# Sourced only by the owned transport fixture runner.
check_forwarding_peers() {
    local hop=$stack-forwarder image binding hop_ip upstream expected phase
    image=$(engine container inspect "$proxy" | jq -er '.[0].Image')
    containers+=("$hop")
    engine create --name "$hop" --label "io.durpdeploy.transport-run=$run" \
        --network "$stack" --publish 127.0.0.1::443 --cap-drop=ALL --cap-add=NET_BIND_SERVICE \
        --security-opt=no-new-privileges --read-only --memory=128m --cpus=0.5 --pids-limit=64 \
        --tmpfs /tmp:rw,size=32m --volume "$stack-caddy-config:/config:ro" \
        "$image" sh -c 'for i in $(seq 1 60); do
            if test -s /config/forwarder.Caddyfile; then exec caddy run --config /config/forwarder.Caddyfile --adapter caddyfile; fi
            sleep 1
        done; exit 1' >/dev/null
    if [[ $runtime == docker ]]; then engine network connect "$stack-ingress" "$hop"; fi
    engine start "$hop" >/dev/null
    binding=$(engine port "$hop" 443/tcp)
    [[ $binding =~ ^127\.0\.0\.1:([0-9]+)$ ]] || return 1
    local forward_url=https://$stack.localhost:${BASH_REMATCH[1]}
    hop_ip=$(engine container inspect "$hop" | jq -er --arg net "$stack" '.[0].NetworkSettings.Networks[$net].IPAddress')
    engine cp --archive=false "$private/fixture-ca.pem" "$proxy:/config/forward-ca.pem"
    for phase in untrusted-app untrusted-caddy trusted-caddy; do
        expected=$hop_ip
        upstream=$app_ip:8080
        if [[ $phase != untrusted-app ]]; then upstream=$proxy_ip:443; fi
        if [[ $phase == trusted-caddy ]]; then
            expected=198.51.100.18
            engine exec --env "DURPDEPLOY_CADDY_TRUSTED_PROXIES=$hop_ip/32" "$proxy" \
                caddy adapt --config /config/Caddyfile --adapter caddyfile 2>"$private/trusted-adapt.log" |
                jq '.apps.tls.certificates.load_files=[{certificate:"/config/cert.pem",key:"/config/key.pem"}] |
                    .apps.http.servers[].automatic_https.disable_certificates=true' >"$private/trusted.json"
            engine cp --archive=false "$private/trusted.json" "$proxy:/config/trusted.json"
            engine exec "$proxy" caddy reload --force --config /config/trusted.json >"$private/trusted-reload.log" 2>&1
        fi
        {
            printf '{\n auto_https disable_certs\n}\n:443 {\n tls /config/cert.pem /config/key.pem\n'
            printf ' reverse_proxy %s {\n' "$upstream"
            printf '  header_up X-Forwarded-For "203.0.113.77, 198.51.100.18"\n'
            printf '  header_up X-Forwarded-Proto "http"\n'
            printf '  header_up Host "%s.localhost"\n' "$stack"
            if [[ $phase != untrusted-app ]]; then
                printf '  transport http {\n   tls_trust_pool file /config/forward-ca.pem\n'
                printf '   tls_server_name %s.localhost\n  }\n' "$stack"
            fi
            printf ' }\n}\n'
        } >"$private/forwarder.Caddyfile"
        engine cp --archive=false "$private/forwarder.Caddyfile" "$proxy:/config/forwarder.Caddyfile"
        if [[ $phase == untrusted-app ]]; then
            local deadline=$((SECONDS+30)) ready=0
            while ((SECONDS<deadline)); do
                if engine exec "$hop" caddy reload --force --config /config/forwarder.Caddyfile \
                    --adapter caddyfile >"$private/hop-reload.log" 2>&1; then ready=1; break; fi
                sleep 0.25
            done
            ((ready)) || return 1
        else
            engine exec "$hop" caddy reload --force --config /config/forwarder.Caddyfile \
                --adapter caddyfile >"$private/hop-reload.log" 2>&1
        fi
        jq --arg url "$forward_url" --arg expected "$expected" --arg mode "$phase" \
            '. + {forward_url:$url,expected_ip:$expected,forward_mode:$mode}' \
            "$private/fixture.json" >"$private/forwarding.json"
        HTTPS_SECURITY_FIXTURE="$private/forwarding.json" bash scripts/go_test.sh \
            -tags=e2e,transportsecurity,forwardingsecurity -count=1 -timeout=1m \
            -run '^TestTransportForwardingPeerE2E$' ./internal/handler/api >"$private/forward-$phase.log" 2>&1
    done
    engine exec "$proxy" caddy reload --force --config /config/security.json >"$private/restore.log" 2>&1
    engine rm -f "$hop" >/dev/null
    printf '{"untrusted_app":true,"untrusted_caddy":true,"trusted_chain":true}\n' >"$report/forwarding.json"
}
