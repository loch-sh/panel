import { ThemeConfig, theme } from "antd";

export const spacing = {
  smaller: 5,
  small: 10,
  normal: 15,
  large: 20,
  larger: 25,
};

const commonTokens: ThemeConfig["token"] = {
  fontSizeHeading1: 24,
  fontSizeHeading2: 22,
  fontSizeHeading3: 20,
  fontSizeHeading4: 18,
  fontSize: 14,
  colorPrimary: "#1677ff",
};

export const lightTheme: ThemeConfig = {
  algorithm: theme.defaultAlgorithm,
  token: {
    ...commonTokens,
  },
};

export const darkTheme: ThemeConfig = {
  algorithm: theme.darkAlgorithm,
  token: {
    ...commonTokens,
  },
};
