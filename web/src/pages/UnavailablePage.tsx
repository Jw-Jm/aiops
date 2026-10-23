import { Link } from "react-router-dom";
import { useAuth } from "../auth/AuthProvider";
import { PageHeading } from "../components/PageHeading";
import { Surface, UnavailablePanel } from "../components/v12/primitives";

export function UnavailablePage({
  title,
  description,
  message,
}: {
  title: string;
  description: string;
  message: string;
}) {
  const { session } = useAuth();
  const home = session?.roles.includes("operator")
    ? "/overview"
    : "/access/sources";
  return (
    <>
      <PageHeading title={title} description={description} />
      <Surface title="当前可用性">
        <UnavailablePanel title={title} message={message} />
        <p style={{ margin: "14px 0 0", fontSize: 12 }}>
          <Link to={home}>返回工作台</Link>
          {session?.roles.includes("operator") ? (
            <>
              {" · "}
              <Link to="/resources">查看资源目录</Link>
            </>
          ) : null}
        </p>
      </Surface>
    </>
  );
}
