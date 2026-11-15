import { defineDocs } from 'fumadocs-mdx/macro';

export const docs = defineDocs({
  dir: 'content',
  docs: { async: true },
});
