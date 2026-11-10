import { Button, ConfigProvider, Drawer, Grid, Layout, Skeleton } from "antd";
import { MenuOutlined } from "@ant-design/icons";
import "./App.css";
import "@xterm/xterm/css/xterm.css";
import { Suspense, useState } from "react";
import { Route, Routes } from "react-router";
import { ToastContainer } from "react-toastify";
import AuthGuard from "./Components/AuthGuard";
import ErrorBoundary from "./Components/ErrorBoundary/ErrorBoundary";
import { lazyPage } from "./Utils/lazyPage";
import MenuLayout from "./Components/Layout/MenuLayout";
import useColorScheme from "./hooks/useColorScheme";
import { lightTheme, darkTheme } from "./theme";
import { useAntdLocale } from "./i18n/useLocales";
import { useTranslation } from "react-i18next";

const Containers = lazyPage(() => import("./Pages/Containers/Containers"));
const Cron = lazyPage(() => import("./Pages/Cron/Cron"));
const Domains = lazyPage(() => import("./Pages/Domains/Domains"));
const Home = lazyPage(() => import("./Pages/Home/Home"));
const Users = lazyPage(() => import("./Pages/Users/Users"));
const Sftp = lazyPage(() => import("./Pages/Sftp/Sftp"));
const HostingList = lazyPage(() => import("./Pages/Hosting/HostingList"));
const HostingCreate = lazyPage(() => import("./Pages/Hosting/HostingCreate"));
const HostingDetail = lazyPage(() => import("./Pages/Hosting/HostingDetail"));
const ApiKeys = lazyPage(() => import("./Pages/ApiKeys/ApiKeys"));
const AuditTrail = lazyPage(() => import("./Pages/Audit/AuditTrail"));
const Databases = lazyPage(() => import("./Pages/Databases/Databases"));
const CreateService = lazyPage(() => import("./Pages/Databases/CreateService"));
const LegacyDatabasesRedirect = lazyPage(
  () => import("./Pages/Databases/LegacyDatabasesRedirect"),
);
const FunctionsList = lazyPage(() => import("./Pages/Functions/FunctionsList"));
const CreateFunction = lazyPage(
  () => import("./Pages/Functions/CreateFunction"),
);
const FunctionDetail = lazyPage(
  () => import("./Pages/Functions/FunctionDetail"),
);
const NetworksList = lazyPage(() => import("./Pages/Networks/NetworksList"));
const CreateNetwork = lazyPage(() => import("./Pages/Networks/CreateNetwork"));
const NetworkDetail = lazyPage(() => import("./Pages/Networks/NetworkDetail"));
const DockerNetworkDetail = lazyPage(
  () => import("./Pages/Networks/DockerNetworkDetail"),
);
const PopupTerminal = lazyPage(
  () => import("./Pages/PopupTerminal/PopupTerminal"),
);

const { Sider, Content } = Layout;

function App() {
  const [collapsed, setCollapsed] = useState(false);
  const [mobileMenuOpen, setMobileMenuOpen] = useState(false);
  const screens = Grid.useBreakpoint();
  const isMobile = screens.md === false;
  const colorScheme = useColorScheme();
  const currentTheme = colorScheme === "dark" ? darkTheme : lightTheme;
  const antdLocale = useAntdLocale();
  const { t } = useTranslation("layout");

  return (
    <ConfigProvider theme={currentTheme} locale={antdLocale}>
      <AuthGuard>
        <Suspense fallback={<Skeleton />}>
          <Routes>
            {/* Popup terminal — no sidebar, no padding, fullscreen */}
            <Route
              path="/terminal/:id"
              element={
                <ErrorBoundary>
                  <PopupTerminal />
                </ErrorBoundary>
              }
            />

            {/* Main app with sidebar layout */}
            <Route
              path="*"
              element={
                <Layout style={{ minHeight: "100vh" }}>
                  {!isMobile && (
                    <Sider
                      collapsible
                      collapsed={collapsed}
                      onCollapse={setCollapsed}
                      breakpoint="lg"
                      width={220}
                      collapsedWidth={80}
                      theme={colorScheme === "dark" ? "dark" : "light"}
                    >
                      <MenuLayout collapsed={collapsed} />
                    </Sider>
                  )}
                  <Layout>
                    {isMobile && (
                      <div className="mobile-header">
                        <Button
                          type="text"
                          icon={<MenuOutlined />}
                          aria-label={t("openMenu")}
                          onClick={() => setMobileMenuOpen(true)}
                        />
                      </div>
                    )}
                    <Content
                      className="main-content"
                      style={{
                        padding: 24,
                        overflow: "auto",
                      }}
                    >
                      <ErrorBoundary>
                        {/* Here, so that the menu stays while a page loads. */}
                        <Suspense fallback={<Skeleton />}>
                          <Routes>
                            <Route index element={<Home />} />
                            <Route
                              path="/containers"
                              element={<Containers />}
                            />
                            <Route path="/domains" element={<Domains />} />
                            <Route path="/cron" element={<Cron />} />
                            <Route
                              path="/functions"
                              element={<FunctionsList />}
                            />
                            <Route
                              path="/functions/new"
                              element={<CreateFunction />}
                            />
                            <Route
                              path="/functions/:name"
                              element={<FunctionDetail />}
                            />
                            <Route path="/users" element={<Users />} />
                            <Route path="/sftp" element={<Sftp />} />
                            <Route path="/hosting" element={<HostingList />} />
                            <Route
                              path="/hosting/new"
                              element={<HostingCreate />}
                            />
                            <Route
                              path="/hosting/:name"
                              element={<HostingDetail />}
                            />
                            <Route
                              path="/services/:engine"
                              element={<Databases />}
                            />
                            <Route
                              path="/services/:engine/new"
                              element={<CreateService />}
                            />
                            <Route
                              path="/services/:engine/:name/edit"
                              element={<CreateService />}
                            />
                            <Route
                              path="/databases/:engine"
                              element={<LegacyDatabasesRedirect />}
                            />
                            <Route
                              path="/networks"
                              element={<NetworksList />}
                            />
                            <Route
                              path="/networks/new"
                              element={<CreateNetwork />}
                            />
                            <Route
                              path="/networks/:name"
                              element={<NetworkDetail />}
                            />
                            <Route
                              path="/networks/docker/:id"
                              element={<DockerNetworkDetail />}
                            />
                            <Route path="/api-keys" element={<ApiKeys />} />
                            <Route
                              path="/security/audit"
                              element={<AuditTrail />}
                            />
                          </Routes>
                        </Suspense>
                      </ErrorBoundary>
                    </Content>
                  </Layout>
                  {isMobile && (
                    <Drawer
                      open={mobileMenuOpen}
                      onClose={() => setMobileMenuOpen(false)}
                      placement="left"
                      size={260}
                      styles={{ body: { padding: 0 } }}
                    >
                      <MenuLayout
                        collapsed={false}
                        onNavigate={() => setMobileMenuOpen(false)}
                      />
                    </Drawer>
                  )}
                </Layout>
              }
            />
          </Routes>
        </Suspense>
      </AuthGuard>
      <ToastContainer theme={colorScheme === "dark" ? "dark" : "light"} />
    </ConfigProvider>
  );
}

export default App;
