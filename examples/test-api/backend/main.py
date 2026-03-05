import asyncio
import time

import httpx
from fastapi import FastAPI, Request
from fastapi.middleware.cors import CORSMiddleware

app = FastAPI()


app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],  # <-- Allow any origin
    allow_credentials=True,
    allow_methods=["*"],  # GET, POST, PUT, DELETE, etc.
    allow_headers=["*"],  # Allow any headers
)


@app.get("/api/health")
async def health():
    return {"status": "server is healthy"}


@app.get("/api/fail")
async def fail():
    while True:
        time.sleep(30)


@app.post("/api/echo")
async def echo(request: Request):
    body = await request.body()
    return {
        "method": request.method,
        "url": str(request.url),
        "headers": dict(request.headers),
        "query_params": dict(request.query_params),
        "body": body.decode() if body else None,
    }


@app.get("/api/echo")
async def echo_get(request: Request):
    async with httpx.AsyncClient() as c:
        SERVICE_BASE_URL = "http://auth.auth-nikumar1206.svc.cluster.local:80"
        BALANCER_BASE_URL = "http://test-api.onloco.app"
        await asyncio.sleep(1)
        start_time = time.perf_counter()
        try:
            balancer_r = await c.get(BALANCER_BASE_URL + "/auth")
            balancer_json = balancer_r.json()
        except Exception as e:
            balancer_json = {"error": str(e)}
        end_time = time.perf_counter()

        try:
            svc_r = await c.get(SERVICE_BASE_URL + "/auth")
            svc_json = svc_r.json()
        except Exception as e:
            svc_json = {"error": str(e)}
        service_end_time = time.perf_counter()

    return {
        "method": request.method,
        "url": str(request.url),
        "headers": dict(request.headers),
        "query_params": dict(request.query_params),
        "auth_service_base_url": SERVICE_BASE_URL,
        "auth_service": svc_json,
        "auth_service_response_time": service_end_time - end_time,
        "auth_balancer_base_url": BALANCER_BASE_URL,
        "auth_balancer": balancer_json,
        "auth_balancer_response_time": end_time - start_time,
    }


if __name__ == "__main__":
    import uvicorn

    uvicorn.run(app, host="0.0.0.0", port=8000)
