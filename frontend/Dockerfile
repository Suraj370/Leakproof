# syntax=docker/dockerfile:1
# Build context is the repo root (see docker-compose.yml) so tests can read backend/testdata.
FROM node:24-alpine AS base
RUN corepack enable
WORKDIR /app/frontend
COPY frontend/package.json frontend/pnpm-lock.yaml ./
RUN --mount=type=cache,target=/root/.local/share/pnpm/store pnpm install --frozen-lockfile
COPY frontend/ ./
COPY backend/testdata /app/backend/testdata

# `docker build --target test` runs typecheck + unit tests.
FROM base AS test
RUN pnpm typecheck && pnpm test

FROM base AS build
RUN pnpm build

FROM nginx:1.27-alpine AS runtime
COPY frontend/nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /app/frontend/dist /usr/share/nginx/html
EXPOSE 80
