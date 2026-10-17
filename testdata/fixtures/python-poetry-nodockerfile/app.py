"""DeployIQ fixture: Poetry-managed Flask app."""
from flask import Flask

app = Flask(__name__)


@app.get("/")
def index():
    return "ok"
