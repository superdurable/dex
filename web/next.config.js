/** @type {import('next').NextConfig} */
const nextConfig = {
  reactStrictMode: true,
  // grpc-js and proto-loader are CommonJS native deps; tell Next not to try to bundle them.
  serverExternalPackages: ['@grpc/grpc-js', '@grpc/proto-loader'],
};

module.exports = nextConfig;
