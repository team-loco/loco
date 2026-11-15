import { docsLlms } from "@/lib/source.server";

export async function loader() {
  return (await docsLlms.serve("/llms.txt"))!;
}
