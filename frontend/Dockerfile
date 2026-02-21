# Build stage
FROM node:25-alpine AS builder

WORKDIR /app

# Install dependencies first for better caching
COPY package.json package-lock.json ./
RUN npm ci

# Copy the rest of the app and build
COPY . .
ARG PUBLIC_API_URL
ARG PUBLIC_TILE_SERVER_URL
ENV PUBLIC_API_URL=$PUBLIC_API_URL
ENV PUBLIC_TILE_SERVER_URL=$PUBLIC_TILE_SERVER_URL
RUN npm run build

# Runtime stage: serve static site with nginx
FROM nginx:1.29-alpine AS runner

# Remove default nginx static assets
RUN rm -rf /usr/share/nginx/html/*

# Copy built static site
COPY --from=builder /app/build /usr/share/nginx/html

# Expose nginx port
EXPOSE 80

# Start nginx
CMD ["nginx", "-g", "daemon off;"]
