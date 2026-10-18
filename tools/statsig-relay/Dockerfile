FROM python:3.12-alpine
RUN addgroup -S -g 10002 statsig-relay && adduser -S -D -H -u 10002 -G statsig-relay statsig-relay
COPY tools/statsig-relay/relay.py /usr/local/bin/grok2api-statsig-relay
RUN chmod 0755 /usr/local/bin/grok2api-statsig-relay
USER statsig-relay
EXPOSE 8788
ENTRYPOINT ["/usr/local/bin/grok2api-statsig-relay"]
