FROM node:22-alpine AS build
WORKDIR /app
RUN npm i -g pnpm@10
COPY package.json pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile
COPY . .
RUN pnpm typecheck && pnpm build

FROM node:22-alpine
WORKDIR /app
COPY --from=build /app/.output ./.output
USER node
ENV HOST=0.0.0.0 PORT=2704
EXPOSE 2704
CMD ["node", ".output/server/index.mjs"]
