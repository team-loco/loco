import { Link } from 'react-router';

export default function NotFound() {
  return <main className="mx-auto max-w-xl p-8"><title>Page not found | Loco docs</title><h1>Page not found</h1><p>This documentation page does not exist.</p><Link to="/">Read the documentation</Link></main>;
}
