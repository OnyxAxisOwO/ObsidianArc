<script setup lang="ts">
import { useData, withBase } from 'vitepress';

const { frontmatter } = useData();
</script>

<template>
  <main class="arc-home">
    <section class="arc-hero" aria-labelledby="arc-home-title">
      <div class="arc-hero-copy">
        <p class="arc-eyebrow">{{ frontmatter.home.eyebrow }}</p>
        <h1 id="arc-home-title">{{ frontmatter.home.title }}</h1>
        <p class="arc-hero-subtitle">{{ frontmatter.home.subtitle }}</p>
        <p class="arc-hero-description">{{ frontmatter.home.description }}</p>
        <div class="arc-actions">
          <a class="arc-button primary" :href="withBase(frontmatter.home.primary.link)">
            {{ frontmatter.home.primary.text }} <span aria-hidden="true">↗</span>
          </a>
          <a class="arc-button" :href="frontmatter.home.secondary.link">
            {{ frontmatter.home.secondary.text }}
          </a>
        </div>
        <ul class="arc-facts">
          <li v-for="fact in frontmatter.home.facts" :key="fact">{{ fact }}</li>
        </ul>
      </div>
      <div class="arc-map">
        <div class="arc-map-heading">
          <img :src="withBase('/logo.svg')" alt="" width="24" height="24">
          <span>{{ frontmatter.home.map.title }}</span>
          <span class="arc-tag">{{ frontmatter.home.map.tag }}</span>
        </div>
        <div class="arc-map-inputs">
          <div v-for="entry in frontmatter.home.map.inputs" :key="entry.title">
            <span class="arc-map-symbol" aria-hidden="true">{{ entry.symbol }}</span>
            <strong>{{ entry.title }}</strong>
            <span>{{ entry.description }}</span>
          </div>
        </div>
        <div class="arc-map-connector" aria-hidden="true">↓</div>
        <div class="arc-map-core">
          <strong>Obsidian Arc</strong>
          <span>{{ frontmatter.home.map.core }}</span>
        </div>
        <div class="arc-map-connector" aria-hidden="true">↓</div>
        <div class="arc-map-outputs">
          <span v-for="output in frontmatter.home.map.outputs" :key="output">{{ output }}</span>
        </div>
        <p class="arc-map-caption">{{ frontmatter.home.map.caption }}</p>
      </div>
    </section>

    <section class="arc-paths" aria-labelledby="arc-paths-title">
      <div class="arc-section-heading">
        <h2 id="arc-paths-title">{{ frontmatter.home.pathsTitle }}</h2>
        <p>{{ frontmatter.home.pathsDescription }}</p>
      </div>
      <div class="arc-path-grid">
        <a v-for="(path, index) in frontmatter.home.paths" :key="path.link"
          class="arc-path" :href="withBase(path.link)">
          <span class="arc-path-top"><span>{{ String(index + 1).padStart(2, '0') }}</span><span aria-hidden="true">↗</span></span>
          <h3>{{ path.title }}</h3>
          <p>{{ path.description }}</p>
          <span class="arc-path-label">{{ path.label }}</span>
        </a>
      </div>
    </section>

    <section class="arc-reading" aria-labelledby="arc-reading-title">
      <div>
        <p class="arc-eyebrow">{{ frontmatter.home.readingEyebrow }}</p>
        <h2 id="arc-reading-title">{{ frontmatter.home.readingTitle }}</h2>
        <p>{{ frontmatter.home.readingDescription }}</p>
      </div>
      <div class="arc-reading-links">
        <a v-for="item in frontmatter.home.reading" :key="item.link" :href="withBase(item.link)">
          <span><strong>{{ item.title }}</strong><span>{{ item.description }}</span></span>
          <span aria-hidden="true">↗</span>
        </a>
      </div>
    </section>
  </main>
</template>
