"""DeployIQ fixture: Flask app with an unoptimized single-stage Dockerfile."""
from flask import Flask

app = Flask(__name__)


@app.get("/")
def index():
    return "ok"
