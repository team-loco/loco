FROM node:current-alpine3.23 AS builder

ARG VITE_API_URL
ENV VITE_API_URL=$VITE_API_URL

WORKDIR /app

COPY package.json package-lock.json ./

RUN npm ci

COPY . .

RUN npm run build

FROM nginx:trixie-perl

COPY nginx.conf /etc/nginx/nginx.conf

COPY --from=builder /app/dist /usr/share/nginx/html

EXPOSE 8080

USER 101:101

ENTRYPOINT []
CMD ["nginx", "-g", "daemon off;"]
