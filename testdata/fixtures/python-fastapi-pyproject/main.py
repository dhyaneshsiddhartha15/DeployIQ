"""DeployIQ fixture: FastAPI app declared in pyproject.toml, no Dockerfile."""
from fastapi import FastAPI
from fastapi.responses import PlainTextResponse

app = FastAPI()


@app.get("/", response_class=PlainTextResponse)
def index() -> str:
    return "ok"
