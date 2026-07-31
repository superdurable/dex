/** @type {import('next').NextConfig} */
const nextConfig = {
  reactStrictMode: true,
  serverExternalPackages: ['@grpc/grpc-js', '@grpc/proto-loader'],
};

export default nextConfig;
