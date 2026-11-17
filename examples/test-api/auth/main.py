from fastapi import FastAPI
from fastapi.middleware.cors import CORSMiddleware

app = FastAPI()


app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],  # <-- Allow any origin
    allow_credentials=True,
    allow_methods=["*"],  # GET, POST, PUT, DELETE, etc.
    allow_headers=["*"],  # Allow any headers
)


@app.get("/auth/health")
async def health():
    return {"status": "server is healthy"}


@app.get("/auth")
async def fake_auth():
    # Simulate a successful authentication response
    return {
        "authenticated": True,
        "user": {"id": "12345", "username": "testuser", "roles": ["user"]},
        "token": "fake-jwt-token",
    }


if __name__ == "__main__":
    import uvicorn

    uvicorn.run(app, host="0.0.0.0", port=8000)
