import pytest
from helpers import collector_relays, render_chart


@pytest.fixture(scope="session")
def worker_documents():
    return render_chart("loco-worker", {})


@pytest.fixture(scope="session")
def worker_relays(worker_documents):
    return collector_relays(worker_documents)
