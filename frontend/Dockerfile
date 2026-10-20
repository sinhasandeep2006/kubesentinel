# KubeSentinel UI: static build served by nginx, which also forwards /api to the backend.
FROM node:24-alpine AS build
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci
COPY . .
RUN npm run build

FROM nginx:1.29-alpine
# Backend address; the nginx image fills it into the config at startup.
ENV KS_API_URL=http://kubesentinel-api:8080
COPY nginx.conf.template /etc/nginx/templates/default.conf.template
COPY --from=build /app/dist /usr/share/nginx/html
EXPOSE 80
