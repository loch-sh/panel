import { Spin, theme } from "antd";
import useMe from "../Services/Users/Hooks/useMe";

export default function AuthGuard({ children }: { children: React.ReactNode }) {
  const { isLoading, isError } = useMe();
  const { token } = theme.useToken();

  if (isLoading || isError) {
    return (
      <div
        style={{
          display: "flex",
          justifyContent: "center",
          alignItems: "center",
          height: "100vh",
          backgroundColor: token.colorBgContainer,
        }}
      >
        {isLoading && <Spin size="large" />}
      </div>
    );
  }

  return <>{children}</>;
}
