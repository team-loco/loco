import { index, route, type RouteConfig } from '@react-router/dev/routes';

export default [
  index('routes/docs.tsx', { id: 'overview' }),
  route('api/search', 'routes/search.ts'),
  route('404', 'routes/not-found.tsx'),
  route('*', 'routes/docs.tsx'),
] satisfies RouteConfig;
