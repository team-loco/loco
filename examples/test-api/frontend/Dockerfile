FROM python:3.15.2-alpine@sha256:0f9552bb28711333da16915ec146f5e6bb44a7fdac9ced15dc17c2e0206346dd

COPY . .

EXPOSE 8080

USER 99

CMD ["python", "-m", "http.server", "8080"]
