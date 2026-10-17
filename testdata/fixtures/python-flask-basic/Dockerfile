FROM python:3.12
RUN apt-get update && apt-get install -y build-essential curl
WORKDIR /app
COPY . .
RUN pip install -r requirements.txt
EXPOSE 8000
HEALTHCHECK CMD curl -f http://localhost:8000/ || exit 1
CMD ["gunicorn", "--bind", "0.0.0.0:8000", "app:app"]
