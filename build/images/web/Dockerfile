# The context contains a clean, verified host build (web-dist), this global
# config and the publisher license notices. No dependency install occurs here.
FROM nginxinc/nginx-unprivileged:1.30.0-alpine@sha256:808f7846d21a9c94cf53833e8807a00a33fd0b65cc47fb05b79efe366c2d201f AS upstream
FROM scratch
COPY --from=upstream /usr/sbin/nginx /usr/sbin/nginx
COPY --from=upstream /etc/nginx/mime.types /etc/nginx/mime.types
COPY --from=upstream /lib/ld-musl-x86_64.so.1 /lib/ld-musl-x86_64.so.1
COPY --from=upstream /lib/libc.musl-x86_64.so.1 /lib/libc.musl-x86_64.so.1
COPY --from=upstream /usr/lib/libcrypto.so.3 /usr/lib/libcrypto.so.3
COPY --from=upstream /usr/lib/libssl.so.3 /usr/lib/libssl.so.3
COPY --from=upstream /usr/lib/libpcre2-8.so.0 /usr/lib/libpcre2-8.so.0
COPY --from=upstream /usr/lib/libz.so.1 /usr/lib/libz.so.1
COPY nginx-global.conf /etc/nginx/nginx.conf
COPY web-dist/ /usr/share/nginx/html/
COPY licenses/ /usr/share/ops/licenses/
USER 101:101
EXPOSE 8443
ENTRYPOINT ["/usr/sbin/nginx","-e","/dev/stderr","-g","daemon off;"]
