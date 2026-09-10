import type { Theme } from 'vitepress';
import DefaultTheme from 'vitepress/theme';
import Layout from './Layout.vue';
import OaBadge from './components/OaBadge.vue';
import OaCard from './components/OaCard.vue';
import './styles/index.css';

export const theme: Theme = {
  extends: DefaultTheme,
  Layout,
  enhanceApp({ app }) {
    app.component('OaBadge', OaBadge);
    app.component('OaCard', OaCard);
  },
};

export default theme;
