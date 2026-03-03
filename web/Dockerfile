FROM node:25-alpine AS builder

ARG VITE_API_URL
ARG VITE_APP_ENV
ENV VITE_API_URL=$VITE_API_URL
ENV VITE_APP_ENV=$VITE_APP_ENV

WORKDIR /app

COPY package.json package-lock.json ./
RUN npm ci

COPY . .

RUN apk add --no-cache brotli

RUN npm run build

RUN find dist -type f -size +1k \( \
    -name '*.js' -o \
    -name '*.css' -o \
    -name '*.html' -o \
    -name '*.svg' -o \
    -name '*.json' \
    \) -exec brotli -f -q 9 {} \;


FROM georgjung/nginx-brotli:trixie

COPY nginx.conf /etc/nginx/nginx.conf
COPY --from=builder /app/dist /usr/share/nginx/html

EXPOSE 8080

USER 101:101

ENTRYPOINT []
CMD ["nginx", "-g", "daemon off;"]