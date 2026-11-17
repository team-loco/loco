import os
import time

import aiofiles
import psutil
import requests
from fastapi import FastAPI, Request

app = FastAPI()

# In-memory list to simulate memory growth
memory_hog = []


@app.get("/")
def read_root():
    return {"Hello": "World"}


@app.get("/cpu")
def cpu_load():
    """
    Generates CPU load by performing a computationally intensive task.
    """
    start_time = time.time()
    # Perform a computationally intensive task for a short duration
    while time.time() - start_time < 2:
        _ = [x * x for x in range(10000)]
    return {"message": "CPU load generated for 2 seconds."}


@app.get("/memory")
def memory_load():
    """
    Increases memory usage by appending data to a list.
    """
    # Add 10MB of data to the memory_hog list
    memory_hog.append(" " * 10 * 1024 * 1024)
    process = psutil.Process(os.getpid())
    mem_info = process.memory_info()
    return {
        "message": f"Memory usage increased. Current RSS: {mem_info.rss / 1024 / 1024:.2f} MB"
    }


@app.post("/network/inbound")
async def network_inbound(request: Request):
    """
    Simulates inbound network traffic by receiving data.
    """
    data = await request.body()
    return {"message": f"Received {len(data)} bytes of data."}


@app.get("/network/outbound")
def network_outbound():
    """
    Simulates outbound network traffic by making an external HTTP request.
    """
    try:
        response = requests.get("https://www.google.com", timeout=5)
        return {
            "message": f"Made outbound request to google.com. Status code: {response.status_code}"
        }
    except requests.RequestException as e:
        return {"message": f"Failed to make outbound request: {e}"}


@app.get("/disk")
async def disk_io():
    """
    Simulates disk I/O by writing to and reading from a temporary file.
    """
    file_path = "/tmp/testfile"
    # Write 10MB of data
    data_to_write = b"0" * 10 * 1024 * 1024
    async with aiofiles.open(file_path, "wb") as f:
        await f.write(data_to_write)

    # Read the data back
    async with aiofiles.open(file_path, "rb") as f:
        _ = await f.read()

    os.remove(file_path)
    return {"message": "Disk I/O simulation complete (10MB write/read)."}
