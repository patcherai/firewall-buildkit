# syntax=depthfirst-buildkit:dev

FROM node:24-alpine
WORKDIR /app
COPY package.json ./
RUN npm install
CMD ["node", "-e", "console.log('protected build')"]
