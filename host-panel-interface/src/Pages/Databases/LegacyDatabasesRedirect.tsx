import { Navigate, useParams } from "react-router";

/** The databases moved to /services/:engine (bookmarks keep working). */
export default function LegacyDatabasesRedirect() {
  const { engine = "" } = useParams();
  return <Navigate to={`/services/${encodeURIComponent(engine)}`} replace />;
}
