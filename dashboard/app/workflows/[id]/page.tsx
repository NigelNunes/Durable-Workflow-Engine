import { Timeline } from "@/components/timeline";

export default async function WorkflowPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  return <Timeline id={id} />;
}
