# The kind of Dockerfile DeployIQ exists to fix: full base image, dev
# dependencies installed, npm cache left behind, runs as root.
FROM node:20
WORKDIR /app
COPY . .
RUN npm install
EXPOSE 3000
CMD ["node", "server.js"]
