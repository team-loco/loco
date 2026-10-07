FROM supabase/gotrue:v2.197.0

USER 0:0
RUN apk add --no-cache postgresql18-client=18.6-r0
COPY images/supabase-auth/entrypoint.sh /usr/local/bin/loco-auth-entrypoint
USER 1000:1000

ENTRYPOINT ["loco-auth-entrypoint"]
CMD ["auth"]
